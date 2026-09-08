// Package collector 负责定时读取电表并上报数据。
//
// collector 的中文含义是“采集器”。这个包只关心：
//  1. 什么时候读取；
//  2. 从电表读取什么；
//  3. 把读取结果交给通信客户端。
package collector

import (
	"context"
	"log/slog"
	"time"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/transport"
)

// Collector 保存采集循环运行时需要的依赖。
//
// 把这些依赖放进结构体后，Run和collectAndPublish不需要传很多参数。
type Collector struct {
	// meter是统一电表接口。采集器不需要知道实际品牌或底层协议。
	meter    device.Device
	client   transport.Client
	meterID  int
	interval time.Duration
}

// New 创建采集器，但不会立即启动采集。
func New(
	meter device.Device,
	client transport.Client,
	meterID int,
	interval time.Duration,
) *Collector {
	return &Collector{
		meter:    meter,
		client:   client,
		meterID:  meterID,
		interval: interval,
	}
}

// Run 按照配置的时间间隔循环采集电表。
//
// Run会一直阻塞，直到ctx被取消。app包会把它放进独立goroutine运行。
func (c *Collector) Run(ctx context.Context) {
	// 程序启动后立即采集一次，不需要先等待第一个定时周期。
	c.collectAndPublish()

	// Ticker是周期性定时器，每隔interval向ticker.C发送一个时间值。
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			c.collectAndPublish()
		}
	}
}

// collectAndPublish 完成一次完整采集和上报。
//
// 一次采集包含：
//  1. 电压、电流、有功功率；
//  2. DI1、DI2、DO1、DO2。
func (c *Collector) collectAndPublish() {
	now := time.Now()

	// ============================================================
	// 读取电压、电流和有功功率
	// ============================================================

	measurements, err := c.meter.ReadMeasurements()
	if err != nil {
		slog.Error("采集测量数据失败", "meter_id", c.meterID, "error", err)
	} else {
		message := protocol.MeterData{
			MessageType: protocol.MessageTypeMeterData,
			MeterID:     c.meterID,
			Voltage:     measurements.Voltage,
			Current:     measurements.Current,
			ActivePower: measurements.ActivePower,
			Timestamp:   protocol.FormatTimestamp(now),
		}

		// transport.Client会把内部消息转换后交给Python MQTT服务器。
		if err := c.client.Publish(message); err != nil {
			slog.Error("测量数据上报失败", "meter_id", c.meterID, "error", err)
		}

		slog.Info(
			"测量数据",
			"meter_id", c.meterID,
			"voltage_v", measurements.Voltage,
			"current_ma", measurements.Current,
			"active_power_w", measurements.ActivePower,
		)
	}

	// ============================================================
	// 读取开关量状态
	// ============================================================

	switches, err := c.meter.ReadSwitches()
	if err != nil {
		slog.Error("采集开关量失败", "meter_id", c.meterID, "error", err)
		return
	}

	status := protocol.NewSwitchStatus(c.meterID, switches)
	if err := c.client.Publish(status); err != nil {
		slog.Error("开关状态上报失败", "meter_id", c.meterID, "error", err)
	}

	slog.Info(
		"开关状态",
		"meter_id", c.meterID,
		"di1", switches.DI1,
		"di2", switches.DI2,
		"do1", switches.DO1,
		"do2", switches.DO2,
	)
}
