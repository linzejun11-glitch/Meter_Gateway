// Package modbus 实现可被不同品牌驱动复用的Modbus-RTU主站能力。
package modbus

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"MOCK_COLLECT/common/config"

	"github.com/goburrow/serial"
)

const (
	functionReadHoldingRegisters byte = 0x03
	functionWriteSingleCoil      byte = 0x05
)

// ExceptionError 表示从站返回了Modbus异常响应。
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

// Bus 表示一条物理RS485总线。
//
// 一条总线只能在同一时刻进行一次请求/响应事务。多个电表Client虽然拥有
// 不同Slave ID，但共同使用这里的mutex，因此不会把报文同时写入COM口。
// mutex是mutual exclusion（互斥）的缩写。
type Bus struct {
	port serial.Port

	transactionMu   sync.Mutex
	lastTransaction time.Time
}

// OpenBus 只打开一次共享串口。
func OpenBus(cfg config.RS485Config) (*Bus, error) {
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
	return &Bus{port: port}, nil
}

// Close 在所有电表采集和控制任务停止后关闭共享串口。
func (b *Bus) Close() error {
	if b == nil || b.port == nil {
		return nil
	}
	return b.port.Close()
}

// Client 返回指定从站地址的逻辑客户端，但不会再次打开串口。
func (b *Bus) Client(slaveID byte) *Client {
	return &Client{bus: b, slaveID: slaveID}
}

// Client 表示共享总线上的一个Modbus从站。
type Client struct {
	bus     *Bus
	slaveID byte
}

// ReadHoldingRegisters 使用功能码03读取保持寄存器。
func (c *Client) ReadHoldingRegisters(address, count uint16) ([]uint16, error) {
	if count == 0 || count > 125 {
		return nil, fmt.Errorf("invalid register count: %d", count)
	}

	// 从构造请求到完整读取响应必须持有同一把总线锁。
	c.bus.transactionMu.Lock()
	defer c.bus.transactionMu.Unlock()

	request := make([]byte, 6)
	request[0] = c.slaveID
	request[1] = functionReadHoldingRegisters
	binary.BigEndian.PutUint16(request[2:4], address)
	binary.BigEndian.PutUint16(request[4:6], count)
	request = AppendCRC(request)

	response, err := c.exchange(request, functionReadHoldingRegisters)
	if err != nil {
		return nil, fmt.Errorf(
			"slave=%d read holding registers address=%d count=%d: %w",
			c.slaveID,
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
	for index := 0; index < int(count); index++ {
		start := 3 + index*2
		values[index] = binary.BigEndian.Uint16(response[start : start+2])
	}
	return values, nil
}

func (c *Client) ReadOneRegister(address uint16) (uint16, error) {
	values, err := c.ReadHoldingRegisters(address, 1)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// WriteSingleCoil 使用功能码05写入一个线圈。
func (c *Client) WriteSingleCoil(address uint16, state bool) error {
	c.bus.transactionMu.Lock()
	defer c.bus.transactionMu.Unlock()

	value := uint16(0)
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
		return fmt.Errorf("slave=%d write single coil address=%d: %w", c.slaveID, address, err)
	}
	if len(response) != len(request) {
		return fmt.Errorf(
			"unexpected write response length: want=%d got=%d",
			len(request),
			len(response),
		)
	}
	for index := range request {
		if response[index] != request[index] {
			return fmt.Errorf("write response does not echo request")
		}
	}
	return nil
}

// exchange 完成一次严格的一问一答事务；调用者必须已经锁定共享总线。
func (c *Client) exchange(request []byte, expectedFunction byte) ([]byte, error) {
	c.bus.waitRTUSilentInterval()

	if err := writeAll(c.bus.port, request); err != nil {
		c.bus.lastTransaction = time.Now()
		return nil, fmt.Errorf("serial write: %w", err)
	}
	response, err := c.readResponse(expectedFunction)
	c.bus.lastTransaction = time.Now()
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) readResponse(expectedFunction byte) ([]byte, error) {
	prefix := make([]byte, 3)
	if _, err := io.ReadFull(c.bus.port, prefix); err != nil {
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
		if _, err := io.ReadFull(c.bus.port, crcBytes); err != nil {
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
		remainingLength = byteCount + 2
	case functionWriteSingleCoil:
		remainingLength = 5
	default:
		return nil, fmt.Errorf("unsupported expected function: 0x%02X", expectedFunction)
	}

	remaining := make([]byte, remainingLength)
	if _, err := io.ReadFull(c.bus.port, remaining); err != nil {
		return nil, fmt.Errorf("read remaining response: %w", err)
	}
	frame := append(prefix, remaining...)
	if !VerifyCRC(frame) {
		return nil, fmt.Errorf("invalid Modbus CRC")
	}
	return frame, nil
}

func (b *Bus) waitRTUSilentInterval() {
	if b.lastTransaction.IsZero() {
		return
	}
	const minimumGap = 5 * time.Millisecond
	elapsed := time.Since(b.lastTransaction)
	if elapsed < minimumGap {
		time.Sleep(minimumGap - elapsed)
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
