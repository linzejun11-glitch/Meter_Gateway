package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"MOCK_COLLECT/common/config"

	"github.com/goburrow/serial"
)

const (
	// 本项目用到的 Modbus 功能码。
	functionReadHoldingRegisters byte = 0x03
	functionWriteSingleCoil      byte = 0x05

	// QS300 手册中的寄存器地址。
	registerVoltageA   uint16 = 70
	registerCurrentA   uint16 = 76
	registerPowerA     uint16 = 79
	registerCommConfig uint16 = 301
	registerDOStatus   uint16 = 310
	registerDIStatus   uint16 = 311

	// 写入DO后，每隔200毫秒读取一次地址310，最多确认10次。
	//
	// QS300收到功能码05后会立即回显写入报文，
	// 但继电器状态和地址310可能稍晚才更新。
	// 总确认时间约为2秒，可以避免把“正在更新”误判成控制失败。
	doStateConfirmAttempts = 10
	doStateConfirmInterval = 200 * time.Millisecond
)

var (
	// ErrStateMismatch 表示写命令执行后，读回值与目标值不一致。
	ErrStateMismatch = errors.New("digital output state mismatch")
)

// ExceptionError 表示从站返回了 Modbus 异常响应。
//
// 异常响应的功能码等于原功能码 OR 0x80，第三个字节是异常码。
type ExceptionError struct {
	FunctionCode  byte
	ExceptionCode byte
}

func (e *ExceptionError) Error() string {
	return fmt.Sprintf(
		"modbus exception: function=0x%02X code=0x%02X",
		e.FunctionCode,
		e.ExceptionCode,
	)
}

// Measurements 是从 QS300 读取到的三项基础测量值。
//
// 单位：
//   - Voltage：V（伏）
//   - Current：mA（毫安）
//   - ActivePower：W（瓦）
type Measurements struct {
	Voltage     float64
	Current     float64
	ActivePower float64
}

// SwitchSnapshot 是两路 DI 和两路 DO 的当前状态。
type SwitchSnapshot struct {
	DI1 bool
	DI2 bool
	DO1 bool
	DO2 bool
}

// DeviceInfo 是读取地址 301、304、305 后得到的通信参数。
type DeviceInfo struct {
	SlaveID        uint16
	BaudRateCode   uint16
	DataFormatCode uint16
}

// Client 是本项目自己实现的 Modbus-RTU 主站客户端。
//
// 它只依赖基础串口库，不使用完整 Modbus 库。
// mutex 保证采集线程和控制线程不会同时向 COM3 写报文。
type Client struct {
	port    serial.Port
	slaveID byte
	mutex   sync.Mutex

	// lastTransaction 用于在连续 RTU 报文之间保留短暂静默时间。
	lastTransaction time.Time
}

// Open 打开串口并创建 Modbus 客户端。
func Open(cfg config.MeterConfig) (*Client, error) {
	port, err := serial.Open(&serial.Config{
		Address:  cfg.PortName,
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		Parity:   cfg.Parity,
		StopBits: cfg.StopBits,
		Timeout:  cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", cfg.PortName, err)
	}

	return &Client{
		port:    port,
		slaveID: cfg.SlaveID,
	}, nil
}

// Close 关闭串口，释放 COM3。
func (c *Client) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	return c.port.Close()
}

// ReadHoldingRegisters 手写功能码 03 请求，并解析寄存器响应。
func (c *Client) ReadHoldingRegisters(address, count uint16) ([]uint16, error) {
	if count == 0 || count > 125 {
		return nil, fmt.Errorf("invalid register count: %d", count)
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 请求结构：
	// 从站地址(1) + 功能码(1) + 起始地址(2) + 数量(2) + CRC(2)
	request := make([]byte, 6)
	request[0] = c.slaveID
	request[1] = functionReadHoldingRegisters
	binary.BigEndian.PutUint16(request[2:4], address)
	binary.BigEndian.PutUint16(request[4:6], count)
	request = AppendCRC(request)

	response, err := c.exchange(request, functionReadHoldingRegisters)
	if err != nil {
		return nil, fmt.Errorf(
			"read holding registers address=%d count=%d: %w",
			address,
			count,
			err,
		)
	}

	byteCount := int(response[2])
	expectedByteCount := int(count) * 2
	if byteCount != expectedByteCount {
		return nil, fmt.Errorf(
			"unexpected data length: want=%d got=%d",
			expectedByteCount,
			byteCount,
		)
	}

	values := make([]uint16, count)
	for i := 0; i < int(count); i++ {
		start := 3 + i*2
		values[i] = binary.BigEndian.Uint16(response[start : start+2])
	}

	return values, nil
}

// ReadOneRegister 是读取单个寄存器的便捷函数。
func (c *Client) ReadOneRegister(address uint16) (uint16, error) {
	values, err := c.ReadHoldingRegisters(address, 1)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// WriteSingleCoil 手写功能码 05 请求。
//
// state=true  时写入 0xFF00，表示闭合；
// state=false 时写入 0x0000，表示断开。
func (c *Client) WriteSingleCoil(address uint16, state bool) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	value := uint16(0x0000)
	if state {
		value = 0xFF00
	}

	request := make([]byte, 6)
	request[0] = c.slaveID
	request[1] = functionWriteSingleCoil
	binary.BigEndian.PutUint16(request[2:4], address)
	binary.BigEndian.PutUint16(request[4:6], value)
	request = AppendCRC(request)

	response, err := c.exchange(request, functionWriteSingleCoil)
	if err != nil {
		return fmt.Errorf("write single coil address=%d: %w", address, err)
	}

	// 功能码 05 的正常响应应当原样回显请求。
	if len(response) != len(request) {
		return fmt.Errorf(
			"unexpected write response length: want=%d got=%d",
			len(request),
			len(response),
		)
	}

	for i := range request {
		if response[i] != request[i] {
			return fmt.Errorf("write response does not echo request")
		}
	}

	return nil
}

// ReadMeasurements 读取 A 相电压、电流和有功功率。
func (c *Client) ReadMeasurements() (Measurements, error) {
	voltageRaw, err := c.ReadOneRegister(registerVoltageA)
	if err != nil {
		return Measurements{}, err
	}

	currentRaw, err := c.ReadOneRegister(registerCurrentA)
	if err != nil {
		return Measurements{}, err
	}

	powerRaw, err := c.ReadOneRegister(registerPowerA)
	if err != nil {
		return Measurements{}, err
	}

	return measurementsFromRaw(voltageRaw, currentRaw, powerRaw), nil
}

// measurementsFromRaw 把QS300寄存器原始值换算成对外使用的工程单位。
//
// 电流寄存器的分辨率是0.001A，也就是1mA。因此原始值1250表示
// 1.250A；换算成当前约定的mA后是1250mA。
func measurementsFromRaw(
	voltageRaw uint16,
	currentRaw uint16,
	powerRaw uint16,
) Measurements {
	return Measurements{
		Voltage:     float64(voltageRaw) * 0.1,
		Current:     float64(currentRaw),
		ActivePower: float64(int16(powerRaw)),
	}
}

// ReadSwitches 一次读取连续的 310、311 两个寄存器。
//
// 地址 310 的 bit0/bit1 是两路 DO；
// 地址 311 的 bit0/bit1 是两路 DI。
func (c *Client) ReadSwitches() (SwitchSnapshot, error) {
	values, err := c.ReadHoldingRegisters(registerDOStatus, 2)
	if err != nil {
		return SwitchSnapshot{}, err
	}

	doRaw := values[0]
	diRaw := values[1]

	return SwitchSnapshot{
		DI1: bitIsSet(diRaw, 0),
		DI2: bitIsSet(diRaw, 1),
		DO1: bitIsSet(doRaw, 0),
		DO2: bitIsSet(doRaw, 1),
	}, nil
}

// ControlDigitalOutput 根据业务通道 1/2 控制仪表内部 DO0/DO1。
//
// 返回值：
//   - actualState：地址 310 读回的实际状态
//   - stateKnown：是否成功读到了实际状态
//   - err：写失败、读失败或状态不一致
func (c *Client) ControlDigitalOutput(
	channel int,
	desiredState bool,
) (actualState bool, stateKnown bool, err error) {
	if channel != 1 && channel != 2 {
		return false, false, fmt.Errorf("invalid channel: %d", channel)
	}

	// 业务通道从 1 开始；Modbus 线圈地址从 0 开始。
	coilAddress := uint16(channel - 1)

	if err := c.WriteSingleCoil(coilAddress, desiredState); err != nil {
		return false, false, err
	}

	// 功能码05的正常响应只是回显请求，表示电表已经接收命令，
	// 不代表继电器和状态寄存器已经在这一瞬间完成更新。
	//
	// 因此这里不再“写完立即只读一次”，而是短暂等待并轮询地址310。
	return waitForDigitalOutputState(
		func() (uint16, error) {
			return c.ReadOneRegister(registerDOStatus)
		},
		uint(channel-1),
		desiredState,
		doStateConfirmAttempts,
		doStateConfirmInterval,
	)
}

// waitForDigitalOutputState 轮询DO状态，直到读回值等于期望值。
//
// readStatus作为参数传入，是为了让轮询逻辑可以在不连接真实电表的情况下测试。
// 在正式运行中，它实际调用ReadOneRegister(310)。
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
		// 第一次读取前也等待一个间隔，给电表时间执行继电器动作。
		//
		// 测试时interval可以传0，此时不会真的等待。
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

	// 使用%w包装ErrStateMismatch后，
	// 上层仍然可以通过errors.Is识别并转换为STATE_MISMATCH。
	return actualState, true, fmt.Errorf(
		"%w: wanted=%t got=%t after %d attempts",
		ErrStateMismatch,
		desiredState,
		actualState,
		attempts,
	)
}

// ReadDeviceInfo 一次读取地址 301~305，并取出其中的 301、304、305。
func (c *Client) ReadDeviceInfo() (DeviceInfo, error) {
	values, err := c.ReadHoldingRegisters(registerCommConfig, 5)
	if err != nil {
		return DeviceInfo{}, err
	}

	return DeviceInfo{
		SlaveID:        values[0],
		BaudRateCode:   values[3],
		DataFormatCode: values[4],
	}, nil
}

// exchange 完成一次严格的一问一答串口事务。
//
// 调用者必须已经持有 c.mutex。
func (c *Client) exchange(request []byte, expectedFunction byte) ([]byte, error) {
	c.waitRTUSilentInterval()

	if err := writeAll(c.port, request); err != nil {
		c.lastTransaction = time.Now()
		return nil, fmt.Errorf("serial write: %w", err)
	}

	response, err := c.readResponse(expectedFunction)
	c.lastTransaction = time.Now()
	if err != nil {
		return nil, err
	}

	return response, nil
}

// readResponse 根据功能码读取不同长度的响应。
func (c *Client) readResponse(expectedFunction byte) ([]byte, error) {
	// 先读取前三个字节：
	// 正常 03：从站地址、功能码、数据字节数
	// 异常响应：从站地址、功能码|0x80、异常码
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(c.port, prefix); err != nil {
		return nil, fmt.Errorf("read response prefix: %w", err)
	}

	if prefix[0] != c.slaveID {
		return nil, fmt.Errorf(
			"unexpected slave id: want=%d got=%d",
			c.slaveID,
			prefix[0],
		)
	}

	if prefix[1] == expectedFunction|0x80 {
		crcBytes := make([]byte, 2)
		if _, err := io.ReadFull(c.port, crcBytes); err != nil {
			return nil, fmt.Errorf("read exception CRC: %w", err)
		}

		frame := append(prefix, crcBytes...)
		if !VerifyCRC(frame) {
			return nil, fmt.Errorf("invalid CRC in exception response")
		}

		return nil, &ExceptionError{
			FunctionCode:  expectedFunction,
			ExceptionCode: prefix[2],
		}
	}

	if prefix[1] != expectedFunction {
		return nil, fmt.Errorf(
			"unexpected function: want=0x%02X got=0x%02X",
			expectedFunction,
			prefix[1],
		)
	}

	var remainingLength int
	switch expectedFunction {
	case functionReadHoldingRegisters:
		byteCount := int(prefix[2])
		if byteCount == 0 || byteCount%2 != 0 || byteCount > 250 {
			return nil, fmt.Errorf("invalid byte count: %d", byteCount)
		}

		// 数据区 byteCount 字节，再加 2 字节 CRC。
		remainingLength = byteCount + 2

	case functionWriteSingleCoil:
		// 功能码 05 总响应为 8 字节，已经读了 3 字节。
		remainingLength = 5

	default:
		return nil, fmt.Errorf(
			"unsupported expected function: 0x%02X",
			expectedFunction,
		)
	}

	remaining := make([]byte, remainingLength)
	if _, err := io.ReadFull(c.port, remaining); err != nil {
		return nil, fmt.Errorf("read remaining response: %w", err)
	}

	frame := append(prefix, remaining...)
	if !VerifyCRC(frame) {
		return nil, fmt.Errorf("invalid Modbus CRC")
	}

	return frame, nil
}

// waitRTUSilentInterval 保证连续RTU帧之间至少有约5毫秒间隔。
// 在9600波特率下，这已经大于3.5个字符时间。
func (c *Client) waitRTUSilentInterval() {
	if c.lastTransaction.IsZero() {
		return
	}

	const minimumGap = 5 * time.Millisecond
	elapsed := time.Since(c.lastTransaction)
	if elapsed < minimumGap {
		time.Sleep(minimumGap - elapsed)
	}
}

func bitIsSet(value uint16, bit uint) bool {
	return value&(uint16(1)<<bit) != 0
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
