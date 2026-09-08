// Package app 负责组装并管理整个多电表网关的生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/collector"
	"MOCK_COLLECT/meter/controller"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/driver"
	"MOCK_COLLECT/meter/modbus"
	"MOCK_COLLECT/meter/transport"
)

// runtimeMeter把一块电表的配置和已经创建好的驱动放在一起。
type runtimeMeter struct {
	cfg    config.MeterConfig
	device device.Device
}

// meterSet 表示共享一条RS485总线的全部电表。
type meterSet struct {
	meters   []runtimeMeter
	closeAll func() error
}

func (s *meterSet) Close() error {
	if s == nil || s.closeAll == nil {
		return nil
	}
	return s.closeAll()
}

// Application保存配置以及可替换的创建函数。
//
// openMeters和newClient在正式运行时创建真实串口、MQTT客户端；单元测试会
// 换成内存对象。这种写法叫dependency injection（依赖注入）。
type Application struct {
	cfg config.AppConfig

	openMeters func(config.AppConfig) (*meterSet, error)
	newClient  func(config.AppConfig) (transport.Client, error)
}

// New 创建应用对象，但不会立即打开串口或连接网络。
func New(cfg config.AppConfig) *Application {
	return &Application{
		cfg:        cfg,
		openMeters: openConfiguredMeters,
		newClient:  transport.NewClient,
	}
}

// openConfiguredMeters打开一次串口，然后在这条总线上创建全部电表驱动。
func openConfiguredMeters(cfg config.AppConfig) (*meterSet, error) {
	bus, err := modbus.OpenBus(cfg.RS485)
	if err != nil {
		return nil, err
	}

	meters := make([]runtimeMeter, 0, len(cfg.Meters))
	for _, meterCfg := range cfg.Meters {
		meterDevice, createErr := driver.New(meterCfg, bus)
		if createErr != nil {
			_ = closeRuntimeMeters(meters)
			_ = bus.Close()
			return nil, fmt.Errorf(
				"创建电表驱动失败：meter_id=%d name=%s: %w",
				meterCfg.MeterID,
				meterCfg.Name,
				createErr,
			)
		}
		meters = append(meters, runtimeMeter{cfg: meterCfg, device: meterDevice})
	}

	return &meterSet{
		meters: meters,
		closeAll: func() error {
			// 先释放各型号驱动自己的资源，最后只关闭一次共享串口。
			return errors.Join(closeRuntimeMeters(meters), bus.Close())
		},
	}, nil
}

func closeRuntimeMeters(meters []runtimeMeter) error {
	var result error
	for _, meter := range meters {
		result = errors.Join(result, meter.device.Close())
	}
	return result
}

// Run启动应用，并一直运行到ctx取消或通信客户端异常停止。
func (a *Application) Run(ctx context.Context) error {
	// 即使调用方没有经过LoadAppConfig，也必须在创建外部资源前再次校验。
	if err := a.cfg.Validate(); err != nil {
		return fmt.Errorf("应用配置无效: %w", err)
	}
	printConfig(a.cfg)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	set, err := a.openMeters(a.cfg)
	if err != nil {
		return fmt.Errorf("打开RS485电表集合失败: %w", err)
	}
	defer func() {
		if closeErr := set.Close(); closeErr != nil {
			slog.Error("关闭RS485设备失败", "error", closeErr)
			return
		}
		slog.Info("RS485串口已关闭", "port", a.cfg.RS485.PortName)
	}()

	slog.Info(
		"RS485串口已打开",
		"port", a.cfg.RS485.PortName,
		"meter_count", len(set.meters),
	)
	for _, meter := range set.meters {
		printDeviceInfo(meter.cfg, meter.device)
	}

	client, err := a.newClient(a.cfg)
	if err != nil {
		return err
	}

	var workers sync.WaitGroup
	clientFinished := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		clientFinished <- client.Run(runCtx)
	}()

	slog.Info("正在等待通信客户端就绪")
	select {
	case <-client.Ready():
		slog.Info("通信客户端已就绪，开始多电表采集和控制")
	case clientErr := <-clientFinished:
		workers.Wait()
		if clientErr != nil {
			return fmt.Errorf("通信客户端启动失败: %w", clientErr)
		}
		if runCtx.Err() != nil {
			return nil
		}
		return errors.New("通信客户端在就绪前意外停止")
	case <-runCtx.Done():
		slog.Info("连接尚未就绪时收到退出信号")
		workers.Wait()
		return nil
	}

	// 每块电表拥有独立Ticker，因此可以配置不同采集周期。
	// 它们最终访问同一条Bus，底层互斥锁会自动串行化RTU事务。
	devicesByMeterID := make(map[int]device.Device, len(set.meters))
	for _, meter := range set.meters {
		devicesByMeterID[meter.cfg.MeterID] = meter.device
		meterCollector := collector.New(
			meter.device,
			client,
			meter.cfg.MeterID,
			meter.cfg.CollectInterval,
		)
		workers.Add(1)
		go func() {
			defer workers.Done()
			meterCollector.Run(runCtx)
		}()
	}

	// 只能有一个控制器读取MQTT命令通道，否则多个控制器会争抢命令。
	outputController := controller.New(devicesByMeterID, client)
	workers.Add(1)
	go func() {
		defer workers.Done()
		outputController.Run(runCtx)
	}()

	var runError error
	select {
	case <-runCtx.Done():
		slog.Info("收到退出信号，程序正在停止")
	case clientErr := <-clientFinished:
		if clientErr != nil {
			runError = fmt.Errorf("通信客户端运行失败: %w", clientErr)
		} else {
			runError = errors.New("通信客户端意外停止")
		}
		cancel()
	}

	workers.Wait()
	slog.Info("全部采集、控制和通信任务均已停止")
	return runError
}
