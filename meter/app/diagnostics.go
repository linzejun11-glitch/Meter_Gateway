package app

import (
	"fmt"
	"log/slog"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/device"
)

// printDeviceInfo读取并打印一块电表自身报告的通信参数。
func printDeviceInfo(cfg config.MeterConfig, meter device.Device) {
	info, err := meter.ReadInfo()
	if err != nil {
		slog.Warn(
			"读取仪表通信参数失败",
			"meter_id", cfg.MeterID,
			"name", cfg.Name,
			"slave_id", cfg.SlaveID,
			"error", err,
		)
		return
	}

	baudRates := map[uint16]int{0: 1200, 1: 2400, 2: 4800, 3: 9600}
	dataFormats := map[uint16]string{0: "N.8.1", 1: "O.8.1", 2: "E.8.1"}
	slog.Info(
		"仪表通信参数",
		"meter_id", cfg.MeterID,
		"name", cfg.Name,
		"configured_slave_id", cfg.SlaveID,
		"reported_slave_id", info.SlaveID,
		"baud_rate_code", info.BaudRateCode,
		"reported_baud_rate", baudRates[info.BaudRateCode],
		"data_format_code", info.DataFormatCode,
		"reported_data_format", dataFormats[info.DataFormatCode],
	)
}

// printConfig只打印非敏感运行配置，MQTT密码不会出现在日志中。
func printConfig(cfg config.AppConfig) {
	fmt.Println("电表边缘网关客户端")
	fmt.Println("共享串口：", cfg.RS485.PortName)
	fmt.Println(
		"串口参数：",
		cfg.RS485.BaudRate,
		cfg.RS485.DataBits,
		cfg.RS485.Parity,
		cfg.RS485.StopBits,
	)
	fmt.Println("电表数量：", len(cfg.Meters))
	for _, meter := range cfg.Meters {
		fmt.Printf(
			"  meter_id=%d name=%s driver=%s slave_id=%d interval=%s\n",
			meter.MeterID,
			meter.Name,
			meter.Driver,
			meter.SlaveID,
			meter.CollectInterval,
		)
	}
	fmt.Println("通信方式：Python MQTT Broker")
	fmt.Println("MQTT Broker：", cfg.MQTT.Broker)
	fmt.Println("MQTT网关名称：", cfg.MQTT.DeviceName)
	fmt.Println("上报回复超时：", cfg.MQTT.ReplyTimeout)
}
