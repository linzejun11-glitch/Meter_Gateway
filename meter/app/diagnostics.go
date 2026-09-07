package app

import (
	"fmt"
	"log"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/modbus"
)

// printDeviceInfo 读取并打印电表通信参数。
//
// 这是启动诊断信息，不属于定时采集业务，所以放在app包。
func printDeviceInfo(meter *modbus.Client) {
	info, err := meter.ReadDeviceInfo()
	if err != nil {
		log.Printf("读取仪表通信参数失败：%v", err)
		return
	}

	// 地址304保存的是波特率编码，不是直接保存9600。
	baudRates := map[uint16]int{
		0: 1200,
		1: 2400,
		2: 4800,
		3: 9600,
	}

	// 地址305保存串口数据格式编码。
	dataFormats := map[uint16]string{
		0: "N.8.1",
		1: "O.8.1",
		2: "E.8.1",
	}

	log.Printf(
		"仪表通信参数：地址301=%d 地址304=%d(%d波特) 地址305=%d(%s)",
		info.SlaveID,
		info.BaudRateCode,
		baudRates[info.BaudRateCode],
		info.DataFormatCode,
		dataFormats[info.DataFormatCode],
	)
}

// printConfig 打印当前运行配置，但永远不打印设备密码。
func printConfig(cfg config.AppConfig) {
	fmt.Println("QS300分布式电表采集客户端")
	fmt.Println("串口：", cfg.Meter.PortName)
	fmt.Println(
		"串口参数：",
		cfg.Meter.BaudRate,
		cfg.Meter.DataBits,
		cfg.Meter.Parity,
		cfg.Meter.StopBits,
	)
	fmt.Println("Modbus Slave ID：", cfg.Meter.SlaveID)

	fmt.Println("通信方式：Python MQTT Broker")
	fmt.Println("MQTT Broker：", cfg.MQTT.Broker)
	fmt.Println("MQTT设备名称：", cfg.MQTT.DeviceName)
	fmt.Println("上报回复超时：", cfg.MQTT.ReplyTimeout)
	fmt.Println("MQTT分片大小：", cfg.MQTT.ChunkSize)
	fmt.Println("最大重传轮数：", cfg.MQTT.MaxRetransmits)

	fmt.Println("采集周期：", cfg.CollectInterval)
}
