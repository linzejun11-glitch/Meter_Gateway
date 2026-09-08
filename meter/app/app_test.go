package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/transport"
)

// fakeTransport模拟一条已经就绪的MQTT连接。
type fakeTransport struct {
	ready    chan struct{}
	commands chan protocol.SwitchControl
	stopped  chan struct{}

	mu        sync.Mutex
	published []any
	changed   chan struct{}
}

func newFakeTransport() *fakeTransport {
	ready := make(chan struct{})
	close(ready)
	return &fakeTransport{
		ready:    ready,
		commands: make(chan protocol.SwitchControl),
		stopped:  make(chan struct{}),
		changed:  make(chan struct{}, 1),
	}
}

func (c *fakeTransport) Run(ctx context.Context) error {
	<-ctx.Done()
	close(c.stopped)
	return nil
}

func (c *fakeTransport) Ready() <-chan struct{} { return c.ready }
func (c *fakeTransport) Commands() <-chan protocol.SwitchControl {
	return c.commands
}
func (c *fakeTransport) Publish(message any) error {
	c.mu.Lock()
	c.published = append(c.published, message)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

func (c *fakeTransport) publishedMeterIDs() map[int]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make(map[int]bool)
	for _, message := range c.published {
		switch value := message.(type) {
		case protocol.MeterData:
			ids[value.MeterID] = true
		case protocol.SwitchStatus:
			ids[value.MeterID] = true
		}
	}
	return ids
}

// TestApplicationRunsMultipleMetersWithoutHardware 完整验证应用组装层，
// 但使用模拟电表和模拟MQTT，因此不会访问COM口或服务器。
func TestApplicationRunsMultipleMetersWithoutHardware(t *testing.T) {
	first := &device.Fake{Info: device.Info{SlaveID: 1}}
	second := &device.Fake{Info: device.Info{SlaveID: 2}}
	client := newFakeTransport()
	cfg := config.AppConfig{
		Transport: config.TransportMQTT,
		RS485: config.RS485Config{
			PortName: "COM_TEST", BaudRate: 9600, DataBits: 8,
			Parity: "N", StopBits: 1, Timeout: time.Second,
		},
		Meters: []config.MeterConfig{
			{MeterID: 101, Name: "meter001", Driver: "qs300", SlaveID: 1, CollectInterval: time.Hour},
			{MeterID: 102, Name: "meter002", Driver: "qs300", SlaveID: 2, CollectInterval: time.Hour},
		},
		MQTT: config.MQTTConfig{
			Broker:         "tcp://example.invalid:1883",
			DeviceName:     "gateway-test",
			Password:       "test-password",
			ReplyTimeout:   time.Second,
			ChunkSize:      30,
			MaxRetransmits: 3,
		},
	}

	application := New(cfg)
	application.openMeters = func(config.AppConfig) (*meterSet, error) {
		return &meterSet{
			meters: []runtimeMeter{
				{cfg: cfg.Meters[0], device: first},
				{cfg: cfg.Meters[1], device: second},
			},
			closeAll: func() error {
				_ = first.Close()
				_ = second.Close()
				return nil
			},
		}, nil
	}
	application.newClient = func(config.AppConfig) (transport.Client, error) {
		return client, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- application.Run(ctx) }()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		ids := client.publishedMeterIDs()
		if ids[101] && ids[102] {
			break
		}
		select {
		case <-client.changed:
		case <-deadline.C:
			t.Fatal("两块模拟电表未在规定时间内完成首次上报")
		}
	}

	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("应用停止失败：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("应用未能优雅退出")
	}
	if !first.Closed || !second.Closed {
		t.Fatalf("模拟电表没有全部关闭：first=%t second=%t", first.Closed, second.Closed)
	}
	select {
	case <-client.stopped:
	default:
		t.Fatal("模拟MQTT客户端没有停止")
	}
}
