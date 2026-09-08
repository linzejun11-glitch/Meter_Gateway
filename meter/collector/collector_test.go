package collector

import (
	"context"
	"testing"
	"time"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/device"
)

// recordingClient 是只记录上报内容的内存通信客户端。
// 它实现transport.Client，但不会建立任何网络连接。
type recordingClient struct {
	ready     chan struct{}
	commands  chan protocol.SwitchControl
	published []any
}

func newRecordingClient() *recordingClient {
	ready := make(chan struct{})
	close(ready)
	return &recordingClient{
		ready:    ready,
		commands: make(chan protocol.SwitchControl),
	}
}

func (c *recordingClient) Run(context.Context) error { return nil }
func (c *recordingClient) Ready() <-chan struct{}    { return c.ready }
func (c *recordingClient) Commands() <-chan protocol.SwitchControl {
	return c.commands
}
func (c *recordingClient) Publish(message any) error {
	c.published = append(c.published, message)
	return nil
}

// TestCollectAndPublishWithFakeDevice 验证采集层只依赖统一接口；
// 模拟电表即可完成一次测量值和开关状态上报。
func TestCollectAndPublishWithFakeDevice(t *testing.T) {
	meter := &device.Fake{
		Measurements: device.Measurements{
			Voltage:     220.5,
			Current:     1250,
			ActivePower: 275,
		},
		Switches: device.SwitchSnapshot{
			DI1: true,
			DO2: true,
		},
	}
	client := newRecordingClient()
	collector := New(meter, client, 1, time.Second)

	collector.collectAndPublish()

	if len(client.published) != 2 {
		t.Fatalf("上报条数=%d，期望2", len(client.published))
	}
	measurement, ok := client.published[0].(protocol.MeterData)
	if !ok {
		t.Fatalf("第一条消息类型=%T，期望protocol.MeterData", client.published[0])
	}
	if measurement.MeterID != 1 || measurement.Voltage != 220.5 || measurement.Current != 1250 || measurement.ActivePower != 275 {
		t.Fatalf("测量消息内容错误：%+v", measurement)
	}

	switches, ok := client.published[1].(protocol.SwitchStatus)
	if !ok {
		t.Fatalf("第二条消息类型=%T，期望protocol.SwitchStatus", client.published[1])
	}
	if !switches.DigitalInputs.Channel1 || switches.DigitalInputs.Channel2 || switches.DigitalOutputs.Channel1 || !switches.DigitalOutputs.Channel2 {
		t.Fatalf("开关消息内容错误：%+v", switches)
	}
}
