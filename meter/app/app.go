// Package app 负责组装并管理整个应用的生命周期。
//
// app不实现Modbus、MQTT或TCP协议，它只决定：
//  1. 各模块按什么顺序启动；
//  2. 什么时候认为程序可以开始采集；
//  3. 退出时按什么顺序停止和关闭资源。
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/collector"
	"MOCK_COLLECT/meter/controller"
	"MOCK_COLLECT/meter/modbus"
	"MOCK_COLLECT/meter/transport"
)

// Application 保存运行整个程序所需的总配置。
type Application struct {
	cfg config.AppConfig
}

// New 创建应用对象，但不会打开串口或连接网络。
func New(cfg config.AppConfig) *Application {
	return &Application{cfg: cfg}
}

// Run 启动应用，并一直运行到ctx取消或通信客户端异常停止。
func (a *Application) Run(ctx context.Context) error {
	printConfig(a.cfg)

	// runCtx是应用内部使用的子context。
	//
	// 父ctx通常由Ctrl+C取消；如果网络客户端异常停止，
	// app也可以主动调用cancel，通知采集和控制模块停止。
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	meter, err := modbus.Open(a.cfg.Meter)
	if err != nil {
		return fmt.Errorf("打开电表串口失败: %w", err)
	}

	// 这个defer会在所有工作goroutine退出后执行，
	// 因此关闭COM3时不会再有采集或控制代码访问串口。
	defer func() {
		if closeErr := meter.Close(); closeErr != nil {
			log.Printf("关闭串口失败：%v", closeErr)
			return
		}
		log.Printf("串口已关闭：%s", a.cfg.Meter.PortName)
	}()

	log.Printf("串口已打开：%s", a.cfg.Meter.PortName)
	printDeviceInfo(meter)

	client, err := transport.NewClient(a.cfg)
	if err != nil {
		return err
	}

	// WaitGroup（等待组）统计仍在运行的goroutine数量。
	var workers sync.WaitGroup

	// clientFinished接收通信客户端Run方法的最终返回值。
	// 容量为1可以避免网络goroutine在main暂时未读取时被阻塞。
	clientFinished := make(chan error, 1)

	workers.Add(1)
	go func() {
		defer workers.Done()
		clientFinished <- client.Run(runCtx)
	}()

	log.Printf("正在等待通信客户端就绪")

	// 网络真正就绪前不启动采集，避免第一次上报发生在连接之前。
	select {
	case <-client.Ready():
		log.Printf("通信客户端已就绪，开始采集和控制")

	case clientErr := <-clientFinished:
		// Run已经返回，再等待包装goroutine完成Done。
		workers.Wait()

		if clientErr != nil {
			return fmt.Errorf("通信客户端启动失败: %w", clientErr)
		}
		if runCtx.Err() != nil {
			return nil
		}
		return errors.New("通信客户端在就绪前意外停止")

	case <-runCtx.Done():
		log.Printf("连接尚未就绪时收到退出信号")
		workers.Wait()
		return nil
	}

	// 采集器负责定时读取和上报。
	meterCollector := collector.New(
		meter,
		client,
		a.cfg.Meter.SlaveID,
		a.cfg.CollectInterval,
	)

	// 控制器负责接收命令、操作DO并回复ACK。
	outputController := controller.New(
		meter,
		client,
		a.cfg.Meter.SlaveID,
	)

	workers.Add(1)
	go func() {
		defer workers.Done()
		meterCollector.Run(runCtx)
	}()

	workers.Add(1)
	go func() {
		defer workers.Done()
		outputController.Run(runCtx)
	}()

	var runError error

	select {
	case <-runCtx.Done():
		log.Printf("收到退出信号，程序正在停止")

	case clientErr := <-clientFinished:
		if clientErr != nil {
			runError = fmt.Errorf("通信客户端运行失败: %w", clientErr)
		} else {
			runError = errors.New("通信客户端意外停止")
		}

		// 网络已经停止，继续采集无法上报，所以取消整个应用。
		cancel()
	}

	// 优雅退出顺序：
	//   1. Collector不再发起新的Modbus读取；
	//   2. Controller不再执行DO控制；
	//   3. 网络通信客户端关闭；
	//   4. workers归零；
	//   5. Run返回，defer最后关闭串口。
	workers.Wait()
	log.Printf("采集、控制和通信任务均已停止")

	return runError
}
