// Package qs300 实现QS300电表的型号专用驱动。
//
// 寄存器地址、比例系数、DI/DO位定义和控制确认规则都属于这个型号，
// 因此集中放在这里，不再污染可复用的Modbus-RTU协议层。
package qs300

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/modbus"

	"github.com/goburrow/serial"
)

const (
	registerVoltageA   uint16 = 70
	registerCurrentA   uint16 = 76
	registerPowerA     uint16 = 79
	registerCommConfig uint16 = 301
	registerDOStatus   uint16 = 310
	registerDIStatus   uint16 = 311

	doStateConfirmAttempts = 10
	doStateConfirmInterval = 200 * time.Millisecond
)

// registerClient 是QS300驱动需要的底层寄存器访问能力。
// 使用小接口后，测试可以传入内存中的假总线，不需要COM口和真实电表。
type registerClient interface {
	ReadHoldingRegisters(address, count uint16) ([]uint16, error)
	ReadOneRegister(address uint16) (uint16, error)
	WriteSingleCoil(address uint16, state bool) error
	Close() error
}

// Driver 把QS300寄存器翻译成网关统一的device.Device接口。
type Driver struct {
	bus             registerClient
	confirmAttempts int
	confirmInterval time.Duration
}

// Open 打开Modbus-RTU串口并创建QS300驱动。
func Open(cfg config.MeterConfig) (*Driver, error) {
	bus, err := modbus.Open(cfg)
	if err != nil {
		return nil, normalizeError(err)
	}
	return newDriver(bus, doStateConfirmAttempts, doStateConfirmInterval), nil
}

func newDriver(
	bus registerClient,
	confirmAttempts int,
	confirmInterval time.Duration,
) *Driver {
	return &Driver{
		bus:             bus,
		confirmAttempts: confirmAttempts,
		confirmInterval: confirmInterval,
	}
}

func (d *Driver) Close() error {
	if d == nil || d.bus == nil {
		return nil
	}
	return d.bus.Close()
}

// ReadMeasurements读取三个QS300寄存器并换算为统一工程单位。
func (d *Driver) ReadMeasurements() (device.Measurements, error) {
	voltageRaw, err := d.bus.ReadOneRegister(registerVoltageA)
	if err != nil {
		return device.Measurements{}, normalizeError(err)
	}
	currentRaw, err := d.bus.ReadOneRegister(registerCurrentA)
	if err != nil {
		return device.Measurements{}, normalizeError(err)
	}
	powerRaw, err := d.bus.ReadOneRegister(registerPowerA)
	if err != nil {
		return device.Measurements{}, normalizeError(err)
	}
	return measurementsFromRaw(voltageRaw, currentRaw, powerRaw), nil
}

func measurementsFromRaw(
	voltageRaw uint16,
	currentRaw uint16,
	powerRaw uint16,
) device.Measurements {
	return device.Measurements{
		// QS300电压分辨率为0.1V，所以原始值乘以0.1。
		Voltage: float64(voltageRaw) * 0.1,
		// QS300电流分辨率为0.001A，恰好等于1mA。
		Current: float64(currentRaw),
		// 有功功率允许为负值，因此先转换成有符号int16。
		ActivePower: float64(int16(powerRaw)),
	}
}

// ReadSwitches一次读取连续的310和311两个寄存器。
func (d *Driver) ReadSwitches() (device.SwitchSnapshot, error) {
	values, err := d.bus.ReadHoldingRegisters(registerDOStatus, 2)
	if err != nil {
		return device.SwitchSnapshot{}, normalizeError(err)
	}
	if len(values) != 2 {
		return device.SwitchSnapshot{}, fmt.Errorf(
			"%w: QS300 switch register count=%d, want=2",
			device.ErrProtocol,
			len(values),
		)
	}

	doRaw, diRaw := values[0], values[1]
	return device.SwitchSnapshot{
		DI1: bitIsSet(diRaw, 0),
		DI2: bitIsSet(diRaw, 1),
		DO1: bitIsSet(doRaw, 0),
		DO2: bitIsSet(doRaw, 1),
	}, nil
}

// ControlDigitalOutput把业务通道1/2映射到QS300线圈0/1。
func (d *Driver) ControlDigitalOutput(
	channel int,
	desiredState bool,
) (actualState bool, stateKnown bool, err error) {
	if channel != 1 && channel != 2 {
		return false, false, fmt.Errorf("invalid channel: %d", channel)
	}

	coilAddress := uint16(channel - 1)
	if err := d.bus.WriteSingleCoil(coilAddress, desiredState); err != nil {
		return false, false, normalizeError(err)
	}

	return waitForDigitalOutputState(
		func() (uint16, error) {
			value, readErr := d.bus.ReadOneRegister(registerDOStatus)
			return value, normalizeError(readErr)
		},
		uint(channel-1),
		desiredState,
		d.confirmAttempts,
		d.confirmInterval,
	)
}

func waitForDigitalOutputState(
	readStatus func() (uint16, error),
	bit uint,
	desiredState bool,
	attempts int,
	interval time.Duration,
) (actualState bool, stateKnown bool, err error) {
	if attempts <= 0 {
		return false, false, fmt.Errorf("DO state confirmation attempts must be positive")
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if interval > 0 {
			time.Sleep(interval)
		}
		doRaw, readErr := readStatus()
		if readErr != nil {
			return actualState, stateKnown, fmt.Errorf(
				"read back DO status on attempt %d: %w",
				attempt,
				readErr,
			)
		}
		actualState = bitIsSet(doRaw, bit)
		stateKnown = true
		if actualState == desiredState {
			return actualState, true, nil
		}
	}

	return actualState, true, fmt.Errorf(
		"%w: wanted=%t got=%t after %d attempts",
		device.ErrStateMismatch,
		desiredState,
		actualState,
		attempts,
	)
}

// ReadInfo读取QS300地址301到305，并返回通信配置编码。
func (d *Driver) ReadInfo() (device.Info, error) {
	values, err := d.bus.ReadHoldingRegisters(registerCommConfig, 5)
	if err != nil {
		return device.Info{}, normalizeError(err)
	}
	if len(values) != 5 {
		return device.Info{}, fmt.Errorf(
			"%w: QS300 info register count=%d, want=5",
			device.ErrProtocol,
			len(values),
		)
	}
	return device.Info{
		SlaveID:        values[0],
		BaudRateCode:   values[3],
		DataFormatCode: values[4],
	}, nil
}

// normalizeError把串口和Modbus实现细节转换成业务层统一错误。
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, serial.ErrTimeout) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return fmt.Errorf("%w: %v", device.ErrTimeout, err)
	}

	// 先识别具体的协议错误，再使用较宽泛的ErrProtocol兜底。
	// 这样上层不需要导入Modbus包，仍然可以保留原有的细分错误码。
	var exception *modbus.ExceptionError
	message := strings.ToLower(err.Error())
	if errors.As(err, &exception) {
		return fmt.Errorf("%w: %v", device.ErrDeviceException, err)
	}
	if strings.Contains(message, "crc") {
		return fmt.Errorf("%w: %v", device.ErrChecksum, err)
	}
	if strings.Contains(message, "unexpected") {
		return fmt.Errorf("%w: %v", device.ErrProtocol, err)
	}
	return err
}

func bitIsSet(value uint16, bit uint) bool {
	return value&(uint16(1)<<bit) != 0
}

// 编译期检查：QS300驱动必须完整实现统一电表接口。
var _ device.Device = (*Driver)(nil)
