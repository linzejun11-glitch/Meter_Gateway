// Package collector 负责定时读取电表并上报数据。
//
// collector 的中文含义是“采集器”。这个包只关心：
//  1. 什么时候读取；
//  2. 从电表读取什么；
//  3. 把读取结果交给通信客户端。
package collector

import (
	"context"
	"log"
	"time"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/modbus"
	"MOCK_COLLECT/meter/transport"
)

// Collector 保存采集循环运行时需要的依赖。
//
// 把这些依赖放进结构体后，Run和collectAndPublish不需要传很多参数。
type Collector struct {
	meter    *modbus.Client
	client   transport.Client
	slaveID  byte
	interval time.Duration
}

// New 创建采集器，但不会立即启动采集。
func New(
	meter *modbus.Client,
	client transport.Client,
	slaveID byte,
	interval time.Duration,
) *Collector {
	return &Collector{
		meter:    meter,
		client:   client,
		slaveID:  slaveID,
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
		log.Printf("采集测量数据失败：%v", err)
	} else {
		message := protocol.MeterData{
			MessageType: protocol.MessageTypeMeterData,
			MeterID:     int(c.slaveID),
			Voltage:     measurements.Voltage,
			Current:     measurements.Current,
			ActivePower: measurements.ActivePower,
			Timestamp:   protocol.FormatTimestamp(now),
		}

		// transport.Client会把内部消息转换后交给Python MQTT服务器。
		if err := c.client.Publish(message); err != nil {
			log.Printf("测量数据上报失败：%v", err)
		}

		log.Printf(
			"测量数据：Ua=%.1fV Ia=%.0fmA Pa=%.0fW",
			measurements.Voltage,
			measurements.Current,
			measurements.ActivePower,
		)
	}

	// ============================================================
	// 读取开关量状态
	// ============================================================

	switches, err := c.meter.ReadSwitches()
	if err != nil {
		log.Printf("采集开关量失败：%v", err)
		return
	}

	status := protocol.NewSwitchStatus(c.slaveID, switches)
	if err := c.client.Publish(status); err != nil {
		log.Printf("开关状态上报失败：%v", err)
	}

	log.Printf(
		"开关状态：DI1=%t DI2=%t DO1=%t DO2=%t",
		switches.DI1,
		switches.DI2,
		switches.DO1,
		switches.DO2,
	)
}
