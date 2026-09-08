package protocol

import (
	"time"

	"MOCK_COLLECT/meter/device"
)

// NewSwitchStatus 把电表驱动读取到的开关快照转换为通信层使用的消息。
//
// Modbus层使用DI1、DI2、DO1、DO2表示硬件状态；
// 通信协议使用digital_inputs和digital_outputs组织JSON字段。
// 把转换集中在这里，可以保证定时采集和控制后的补报使用完全相同的格式。
func NewSwitchStatus(
	slaveID byte,
	switches device.SwitchSnapshot,
) SwitchStatus {
	return SwitchStatus{
		MessageType: MessageTypeSwitchStatus,
		MeterID:     int(slaveID),

		DigitalInputs: SwitchChannels{
			Channel1: switches.DI1,
			Channel2: switches.DI2,
		},

		DigitalOutputs: SwitchChannels{
			Channel1: switches.DO1,
			Channel2: switches.DO2,
		},

		Timestamp: FormatTimestamp(time.Now()),
	}
}
