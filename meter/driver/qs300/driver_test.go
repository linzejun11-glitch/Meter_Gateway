package qs300

import (
	"errors"
	"testing"

	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/modbus"
)

// fakeBus 模拟QS300驱动下面的Modbus寄存器访问层。
//
// 单元测试只在内存中读写这些字段，不会打开COM口。这样即使电表不在身边，
// 也能验证寄存器地址、数值换算、DO映射和状态确认等核心规则。
type fakeBus struct {
	registers map[uint16]uint16

	readHoldingAddress uint16
	readHoldingCount   uint16
	writtenCoilAddress uint16
	writtenCoilState   bool
	writeCalls         int
	closeCalls         int

	doReadValues []uint16
	doReadIndex  int
	readError    error
	writeError   error
}

func (b *fakeBus) ReadHoldingRegisters(address, count uint16) ([]uint16, error) {
	b.readHoldingAddress = address
	b.readHoldingCount = count
	if b.readError != nil {
		return nil, b.readError
	}

	values := make([]uint16, count)
	for index := uint16(0); index < count; index++ {
		values[index] = b.registers[address+index]
	}
	return values, nil
}

func (b *fakeBus) ReadOneRegister(address uint16) (uint16, error) {
	if b.readError != nil {
		return 0, b.readError
	}

	// DO状态确认允许每次读取返回不同值，用来模拟电表写入后稍晚才刷新。
	if address == registerDOStatus && b.doReadIndex < len(b.doReadValues) {
		value := b.doReadValues[b.doReadIndex]
		b.doReadIndex++
		return value, nil
	}
	return b.registers[address], nil
}

func (b *fakeBus) WriteSingleCoil(address uint16, state bool) error {
	b.writtenCoilAddress = address
	b.writtenCoilState = state
	b.writeCalls++
	return b.writeError
}

func (b *fakeBus) Close() error {
	b.closeCalls++
	return nil
}

// TestMeasurementsFromRaw 验证QS300原始寄存器值到工程单位的换算。
func TestMeasurementsFromRaw(t *testing.T) {
	got := measurementsFromRaw(2205, 1250, 65526)

	if got.Voltage != 220.5 {
		t.Fatalf("voltage=%v，期望220.5V", got.Voltage)
	}
	if got.Current != 1250 {
		t.Fatalf("current=%v，期望1250mA", got.Current)
	}
	if got.ActivePower != -10 {
		t.Fatalf("active_power=%v，期望-10W", got.ActivePower)
	}
}

// TestReadSwitches 验证310是DO、311是DI，并验证各状态位的含义。
func TestReadSwitches(t *testing.T) {
	bus := &fakeBus{registers: map[uint16]uint16{
		registerDOStatus: 0b10,
		registerDIStatus: 0b01,
	}}
	driver := newDriver(bus, 1, 0)

	got, err := driver.ReadSwitches()
	if err != nil {
		t.Fatalf("ReadSwitches返回意外错误：%v", err)
	}
	if bus.readHoldingAddress != registerDOStatus || bus.readHoldingCount != 2 {
		t.Fatalf("读取范围=%d/%d，期望从310连续读取2个寄存器", bus.readHoldingAddress, bus.readHoldingCount)
	}
	if !got.DI1 || got.DI2 || got.DO1 || !got.DO2 {
		t.Fatalf("开关状态解析错误：%+v", got)
	}
}

// TestControlDigitalOutputMapsChannelAndWaitsForUpdate 验证业务DO2会写线圈1，
// 并允许状态寄存器在几次轮询后才更新。
func TestControlDigitalOutputMapsChannelAndWaitsForUpdate(t *testing.T) {
	bus := &fakeBus{
		registers:    map[uint16]uint16{},
		doReadValues: []uint16{0b00, 0b00, 0b10},
	}
	driver := newDriver(bus, 3, 0)

	actual, known, err := driver.ControlDigitalOutput(2, true)
	if err != nil {
		t.Fatalf("延迟更新最终成功时不应返回错误：%v", err)
	}
	if bus.writeCalls != 1 || bus.writtenCoilAddress != 1 || !bus.writtenCoilState {
		t.Fatalf("线圈写入错误：calls=%d address=%d state=%t", bus.writeCalls, bus.writtenCoilAddress, bus.writtenCoilState)
	}
	if !known || !actual || bus.doReadIndex != 3 {
		t.Fatalf("状态确认错误：actual=%t known=%t reads=%d", actual, known, bus.doReadIndex)
	}
}

// TestControlDigitalOutputReturnsMismatch 验证达到最大轮询次数后，
// 驱动会返回上层可统一识别的状态不一致错误。
func TestControlDigitalOutputReturnsMismatch(t *testing.T) {
	bus := &fakeBus{
		registers:    map[uint16]uint16{},
		doReadValues: []uint16{1, 1, 1},
	}
	driver := newDriver(bus, 3, 0)

	actual, known, err := driver.ControlDigitalOutput(1, false)
	if !errors.Is(err, device.ErrStateMismatch) {
		t.Fatalf("期望ErrStateMismatch，实际：%v", err)
	}
	if !known || !actual {
		t.Fatalf("最后一次读回状态错误：actual=%t known=%t", actual, known)
	}
}

// TestReadInfo 验证通信信息使用301、304和305寄存器。
func TestReadInfo(t *testing.T) {
	bus := &fakeBus{registers: map[uint16]uint16{
		301: 1,
		304: 3,
		305: 0,
	}}
	driver := newDriver(bus, 1, 0)

	got, err := driver.ReadInfo()
	if err != nil {
		t.Fatalf("ReadInfo返回意外错误：%v", err)
	}
	if got.SlaveID != 1 || got.BaudRateCode != 3 || got.DataFormatCode != 0 {
		t.Fatalf("通信信息解析错误：%+v", got)
	}
}

// TestCloseDoesNotCloseSharedBus 验证关闭单块电表驱动不会关闭共享总线。
func TestCloseDoesNotCloseSharedBus(t *testing.T) {
	bus := &fakeBus{registers: map[uint16]uint16{}}
	driver := newDriver(bus, 1, 0)

	if err := driver.Close(); err != nil {
		t.Fatalf("Close返回意外错误：%v", err)
	}
	if bus.closeCalls != 0 {
		t.Fatalf("单块电表不能关闭共享总线，实际调用次数=%d", bus.closeCalls)
	}
}

// TestNormalizeError 验证QS300底层实现错误会被转换成统一电表错误。
// 上层因此不需要导入串口库或Modbus包。
func TestNormalizeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "超时",
			err:  errors.New("serial read timeout"),
			want: device.ErrTimeout,
		},
		{
			name: "设备异常响应",
			err: &modbus.ExceptionError{
				FunctionCode:  3,
				ExceptionCode: 2,
			},
			want: device.ErrDeviceException,
		},
		{
			name: "CRC校验失败",
			err:  errors.New("invalid Modbus CRC"),
			want: device.ErrChecksum,
		},
		{
			name: "响应格式错误",
			err:  errors.New("unexpected function"),
			want: device.ErrProtocol,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeError(test.err)
			if !errors.Is(got, test.want) {
				t.Fatalf("normalizeError(%v)=%v，期望可识别为%v", test.err, got, test.want)
			}
		})
	}
}
