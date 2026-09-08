package modbus

import (
	"sync"
	"testing"

	"github.com/goburrow/serial"
)

// fakeSerialPort会根据收到请求中的Slave ID立即生成合法响应。
// 它还会记录上一条响应未读完前是否出现了第二次写入。
type fakeSerialPort struct {
	mu sync.Mutex

	response      []byte
	inTransaction bool
	overlapped    bool
	seenSlaves    map[byte]int
}

func (p *fakeSerialPort) Open(*serial.Config) error { return nil }

func (p *fakeSerialPort) Write(request []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inTransaction {
		p.overlapped = true
	}
	p.inTransaction = true
	p.seenSlaves[request[0]]++

	response := []byte{request[0], functionReadHoldingRegisters, 2, 0, 1}
	p.response = AppendCRC(response)
	return len(request), nil
}

func (p *fakeSerialPort) Read(target []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	written := copy(target, p.response)
	p.response = p.response[written:]
	if len(p.response) == 0 {
		p.inTransaction = false
	}
	return written, nil
}

func (p *fakeSerialPort) Close() error { return nil }

// TestClientsShareOneSerializedBus 验证不同Slave ID的客户端共享同一把锁。
func TestClientsShareOneSerializedBus(t *testing.T) {
	port := &fakeSerialPort{seenSlaves: make(map[byte]int)}
	bus := &Bus{port: port}
	first := bus.Client(1)
	second := bus.Client(2)

	var workers sync.WaitGroup
	for _, client := range []*Client{first, second} {
		client := client
		workers.Add(1)
		go func() {
			defer workers.Done()
			for attempt := 0; attempt < 3; attempt++ {
				value, err := client.ReadOneRegister(70)
				if err != nil {
					t.Errorf("读取失败：%v", err)
					return
				}
				if value != 1 {
					t.Errorf("寄存器值=%d，期望1", value)
					return
				}
			}
		}()
	}
	workers.Wait()

	port.mu.Lock()
	defer port.mu.Unlock()
	if port.overlapped {
		t.Fatal("检测到两个从站事务重叠，共享总线互斥失效")
	}
	if port.seenSlaves[1] != 3 || port.seenSlaves[2] != 3 {
		t.Fatalf("从站请求次数错误：%v", port.seenSlaves)
	}
}
