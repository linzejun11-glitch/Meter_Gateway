// Package modbus 实现可被不同品牌驱动复用的 Modbus-RTU 主站能力。
//
// 这个包只处理串口、功能码、报文和CRC，不知道某个寄存器代表电压还是DO。
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
// 异常响应的功能码等于原功能码OR 0x80，第三个字节是异常码。
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

// Client 是通用的Modbus-RTU主站客户端。
// mutex保证多个上层任务不会同时向同一个串口发送报文。
type Client struct {
	port    serial.Port
	slaveID byte
	mutex   sync.Mutex

	// lastTransaction用于保证连续RTU帧之间的静默时间。
	lastTransaction time.Time
}

// Open 根据配置打开串口并创建Modbus客户端。
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

	return &Client{port: port, slaveID: cfg.SlaveID}, nil
}

// Close 关闭底层串口。
func (c *Client) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	return c.port.Close()
}

// ReadHoldingRegisters 使用功能码03读取保持寄存器。
func (c *Client) ReadHoldingRegisters(address, count uint16) ([]uint16, error) {
	if count == 0 || count > 125 {
		return nil, fmt.Errorf("invalid register count: %d", count)
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 请求：从站(1)+功能码(1)+起始地址(2)+数量(2)+CRC(2)。
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

// ReadOneRegister 是读取单个保持寄存器的便捷方法。
func (c *Client) ReadOneRegister(address uint16) (uint16, error) {
	values, err := c.ReadHoldingRegisters(address, 1)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// WriteSingleCoil 使用功能码05写入一个线圈。
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

	// 功能码05的正常响应必须原样回显请求。
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

// exchange 完成一次严格的一问一答串口事务。
// 调用者必须已经持有c.mutex。
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

func (c *Client) readResponse(expectedFunction byte) ([]byte, error) {
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
		remainingLength = byteCount + 2
	case functionWriteSingleCoil:
		// 功能码05响应总长8字节，前3字节已经读取。
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

// waitRTUSilentInterval 保证连续RTU帧之间至少约5毫秒静默时间。
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
