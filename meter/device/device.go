// Package device 定义网关业务层认可的统一电表能力。
//
// collector 和 controller 只依赖这里的 Device 接口，不再依赖 QS300、
// Modbus 或串口。以后接入其他品牌时，只需增加一个实现该接口的驱动。
package device

import "errors"

var (
	// ErrTimeout 表示电表在规定时间内没有完成通信。
	ErrTimeout = errors.New("meter communication timeout")

	// ErrProtocol 表示响应报文、CRC或设备异常响应不符合协议。
	ErrProtocol = errors.New("meter protocol error")

	// ErrDeviceException 表示设备明确返回了“无法执行请求”的异常响应。
	// 例如Modbus从站返回异常功能码时，驱动应包装这个错误。
	ErrDeviceException = errors.New("meter returned an exception")

	// ErrChecksum 表示响应已经收到，但校验和（例如CRC）不正确。
	ErrChecksum = errors.New("meter response checksum error")

	// ErrStateMismatch 表示控制后读回的实际状态与目标状态不一致。
	ErrStateMismatch = errors.New("digital output state mismatch")
)

// Measurements 是网关统一使用的电表测量数据。
// 不同品牌驱动必须先换算成这里规定的工程单位。
type Measurements struct {
	Voltage     float64 // V，伏特
	Current     float64 // mA，毫安
	ActivePower float64 // W，瓦特
}

// SwitchSnapshot 是网关当前支持的两路DI和两路DO状态。
type SwitchSnapshot struct {
	DI1 bool
	DI2 bool
	DO1 bool
	DO2 bool
}

// Info 是用于启动诊断的电表通信信息。
// Code字段保存设备协议中的原始编码，便于现场核对设备设置。
type Info struct {
	SlaveID        uint16
	BaudRateCode   uint16
	DataFormatCode uint16
}

// Device 是所有电表驱动必须实现的统一接口。
//
// 接口只描述网关需要电表完成什么工作，不规定工作怎样完成。因此实现可以
// 使用Modbus-RTU、Modbus-TCP或其他厂商协议。
type Device interface {
	ReadMeasurements() (Measurements, error)
	ReadSwitches() (SwitchSnapshot, error)
	ControlDigitalOutput(
		channel int,
		desiredState bool,
	) (actualState bool, stateKnown bool, err error)
	ReadInfo() (Info, error)
	Close() error
}
