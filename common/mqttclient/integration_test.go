package mqttclient

import (
	"context"
	"os"
	"testing"
	"time"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/protocol"
)

// TestPythonBrokerIntegration 需要先启动同事的Python MQTT Broker。
//
// 普通go test会跳过它；联调时设置：
//
//	PYTHON_MQTT_INTEGRATION=1
func TestPythonBrokerIntegration(t *testing.T) {
	if os.Getenv("PYTHON_MQTT_INTEGRATION") != "1" {
		t.Skip("未启用Python MQTT Broker集成测试")
	}

	client, cancel, runFinished := startIntegrationClient(t)

	// current按照双方最新约定使用mA。
	err := client.Publish(protocol.MeterData{
		MeterID:     1,
		Voltage:     3.31,
		Current:     123,
		ActivePower: 10.25,
		Timestamp:   "2026-07-28 12:34:56",
	})
	if err != nil {
		t.Fatalf("向Python Broker上报失败：%v", err)
	}

	stopIntegrationClient(t, cancel, runFinished)
}

func TestPythonBrokerSelectiveRetransmission(t *testing.T) {
	if os.Getenv("PYTHON_MQTT_INTEGRATION") != "1" {
		t.Skip("未启用Python MQTT Broker集成测试")
	}

	client, cancel, runFinished := startIntegrationClient(t)

	sessionID, businessID, replies, err := client.registerPending()
	if err != nil {
		t.Fatal(err)
	}
	defer client.removePending(sessionID, businessID)

	payload, err := marshalPostMessage(businessID, protocol.MeterData{
		MeterID:     1,
		Voltage:     3.31,
		Current:     123,
		ActivePower: 10.25,
		Timestamp:   "2026-07-28 12:34:56",
	})
	if err != nil {
		t.Fatal(err)
	}
	frames, err := buildFrames(sessionID, payload, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 3 {
		t.Fatalf("测试消息只有%d帧，无法验证选择重传", len(frames))
	}

	// 故意跳过idx=1，末帧到达后Python应返回cmd=2。
	initialIndexes := make([]int, 0, len(frames)-1)
	for index := range frames {
		if index != 1 {
			initialIndexes = append(initialIndexes, index)
		}
	}
	if err := client.publishFrames(frames, initialIndexes); err != nil {
		t.Fatal(err)
	}

	reply, err := client.waitPostReply(sessionID, replies)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Cmd != 2 || len(reply.Loss) != 1 || reply.Loss[0] != 1 {
		t.Fatalf("补发回复=%+v，期望cmd=2 loss=[1]", reply)
	}

	if err := client.publishFrames(frames, []int{1}); err != nil {
		t.Fatal(err)
	}
	reply, err = client.waitPostReply(sessionID, replies)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Cmd != 0 {
		t.Fatalf("补发后回复=%+v，期望cmd=0", reply)
	}

	stopIntegrationClient(t, cancel, runFinished)
}

// TestPythonBrokerSetIntegration 需要配合会主动调用send_set的Python测试进程。
//
// 普通go test会跳过它；联调时设置：
//
//	PYTHON_MQTT_SET_INTEGRATION=1
func TestPythonBrokerSetIntegration(t *testing.T) {
	if os.Getenv("PYTHON_MQTT_SET_INTEGRATION") != "1" {
		t.Skip("未启用Python MQTT SET集成测试")
	}

	client, cancel, runFinished := startIntegrationClient(t)

	var command protocol.SwitchControl
	select {
	case command = <-client.Commands():
	case <-time.After(10 * time.Second):
		t.Fatal("等待Python下发SET命令超时")
	}

	if !validBusinessID(command.RequestID) {
		t.Fatalf("SET业务id=%q，不是13位数字字符串", command.RequestID)
	}
	if command.Channel != 2 || command.DesiredState == nil || !*command.DesiredState {
		t.Fatalf("SET命令=%+v，期望打开DO2", command)
	}

	if err := client.Publish(protocol.ControlACK{
		MessageType: protocol.MessageTypeControlACK,
		RequestID:   command.RequestID,
		MeterID:     command.MeterID,
		Channel:     command.Channel,
		Success:     true,
		Timestamp:   protocol.FormatTimestamp(time.Now()),
	}); err != nil {
		t.Fatalf("向Python回复SET执行结果失败：%v", err)
	}

	stopIntegrationClient(t, cancel, runFinished)
}

func startIntegrationClient(
	t *testing.T,
) (*Client, context.CancelFunc, <-chan error) {
	t.Helper()

	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		broker = "tcp://127.0.0.1:1883"
	}

	client := NewClient(
		config.MQTTConfig{
			Broker:         broker,
			DeviceName:     "dev1",
			Password:       "123456",
			ReplyTimeout:   5 * time.Second,
			ChunkSize:      30,
			MaxRetransmits: 3,
		},
		[]int{1},
	)

	ctx, cancel := context.WithCancel(context.Background())
	runFinished := make(chan error, 1)
	go func() {
		runFinished <- client.Run(ctx)
	}()

	select {
	case <-client.Ready():
	case err := <-runFinished:
		t.Fatalf("MQTT客户端在就绪前停止：%v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("等待MQTT客户端就绪超时")
	}

	return client, cancel, runFinished
}

func stopIntegrationClient(
	t *testing.T,
	cancel context.CancelFunc,
	runFinished <-chan error,
) {
	t.Helper()
	cancel()
	select {
	case err := <-runFinished:
		if err != nil {
			t.Fatalf("停止MQTT客户端失败：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MQTT客户端未及时停止")
	}
}
