package mqttclient

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/protocol"
)

func TestMarshalMeterDataPostMessage(t *testing.T) {
	payload, err := marshalPostMessage("0000000000009", protocol.MeterData{
		MeterID: 1, Voltage: 220.5, Current: 1250, ActivePower: 275,
		Timestamp: "2026-07-28 12:34:56",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got postMessage
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "0000000000009" || got.Params["meter_id"] != float64(1) {
		t.Fatalf("上报标识错误：%+v", got)
	}
	if got.Params["current"] != float64(1250) ||
		got.Params["collect_time"] != "20260728123456" {
		t.Fatalf("测量属性错误：%+v", got.Params)
	}
}

func TestMarshalSwitchStatusUsesZeroAndOneStrings(t *testing.T) {
	payload, err := marshalPostMessage("0000000000010", protocol.SwitchStatus{
		MeterID:        1,
		DigitalInputs:  protocol.SwitchChannels{Channel1: true},
		DigitalOutputs: protocol.SwitchChannels{Channel2: true},
		Timestamp:      "2026-07-28 12:34:56",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got postMessage
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"di1": "1", "di2": "0", "do1": "0", "do2": "1"}
	for key, value := range want {
		if got.Params[key] != value {
			t.Fatalf("%s=%v，期望%s", key, got.Params[key], value)
		}
	}
}

func TestBuildFrames(t *testing.T) {
	frames, err := buildFrames(11, []byte("abcdefghijklmnopqrstuvwxyz"), 10)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []uint16{
		protocol.MQTTFrameFirst,
		protocol.MQTTFrameMiddle,
		protocol.MQTTFrameLast,
	}
	if len(frames) != len(wantTypes) {
		t.Fatalf("帧数=%d", len(frames))
	}
	for index, frame := range frames {
		if got := binary.BigEndian.Uint16(frame[0:2]); got != wantTypes[index] {
			t.Fatalf("第%d帧类型=%04X", index, got)
		}
	}
}

func TestDecodeSetRequestSupportsElectricMeterValues(t *testing.T) {
	tests := []struct {
		payload string
		channel int
		state   bool
	}{
		{`{"id":"0000000000021","params":{"do1":"1"}}`, 1, true},
		{`{"id":"0000000000022","params":{"do2":false}}`, 2, false},
		{`{"id":"0000000000023","params":{"do1":0}}`, 1, false},
	}
	for _, test := range tests {
		command, err := decodeSetRequest([]byte(test.payload), []int{1})
		if err != nil {
			t.Fatal(err)
		}
		if command.Channel != test.channel || command.DesiredState == nil ||
			*command.DesiredState != test.state {
			t.Fatalf("控制命令解析错误：%+v", command)
		}
	}
}

func TestDecodeSetRequestRejectsInvalidCommands(t *testing.T) {
	invalid := []string{
		`{"id":"0000000000025","params":{"do1":"1","do2":"0"}}`,
		`{"id":"0000000000026","params":{"do3":"1"}}`,
		`{"id":27,"params":{"do1":"1"}}`,
	}
	for _, payload := range invalid {
		if _, err := decodeSetRequest([]byte(payload), []int{1}); err == nil {
			t.Fatalf("无效命令应被拒绝：%s", payload)
		}
	}
}

// TestDecodeSetRequestRoutesMultipleMeters 验证共享Topic上的meter_id路由。
func TestDecodeSetRequestRoutesMultipleMeters(t *testing.T) {
	command, err := decodeSetRequest(
		[]byte(`{"id":"0000000000102","meter_id":102,"params":{"do2":true}}`),
		[]int{101, 102},
	)
	if err != nil {
		t.Fatal(err)
	}
	if command.MeterID != 102 || command.Channel != 2 {
		t.Fatalf("命令路由错误：%+v", command)
	}

	_, err = decodeSetRequest(
		[]byte(`{"id":"0000000000103","params":{"do1":true}}`),
		[]int{101, 102},
	)
	if err == nil {
		t.Fatal("多电表命令缺少meter_id时应返回错误")
	}

	_, err = decodeSetRequest(
		[]byte(`{"id":"0000000000104","meter_id":999,"params":{"do1":true}}`),
		[]int{101, 102},
	)
	if err == nil {
		t.Fatal("未配置的meter_id应返回错误")
	}
}

func TestValidateLossIndexes(t *testing.T) {
	got, err := validateLossIndexes([]int{2, 0, 2}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 0 {
		t.Fatalf("补发列表=%v", got)
	}
	if _, err := validateLossIndexes([]int{3}, 3); err == nil {
		t.Fatal("越界分片序号应返回错误")
	}
}

func TestMarshalSetReply(t *testing.T) {
	requestID, payload, err := marshalSetReply(protocol.ControlACK{
		RequestID: "0000000000041",
		MeterID:   2,
		Success:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var reply setReply
	if err := json.Unmarshal(payload, &reply); err != nil {
		t.Fatal(err)
	}
	if requestID != reply.ID || reply.MeterID != 2 || reply.Code != 0 || reply.Msg != "success" {
		t.Fatalf("控制回复=%+v", reply)
	}
}

func TestBusinessAndSessionRepliesMatchSamePendingPost(t *testing.T) {
	client := NewClient(testMQTTConfig(), []int{1})
	client.nextSessionID = ^uint16(0)
	sessionID, businessID, replies, err := client.registerPending()
	if err != nil {
		t.Fatal(err)
	}
	defer client.removePending(sessionID, businessID)
	if sessionID != 0 || !validBusinessID(businessID) {
		t.Fatalf("session=%d business=%q", sessionID, businessID)
	}
	sid := sessionID
	bySession, ok := client.findPending(postReply{SessionID: &sid, Cmd: 2})
	if !ok || bySession.replies != replies {
		t.Fatal("未能按session_id匹配上报")
	}
	byBusiness, ok := client.findPending(postReply{ID: businessID, Cmd: 0})
	if !ok || byBusiness.replies != replies {
		t.Fatal("未能按业务id匹配上报")
	}
}

// TestPublishWhileDisconnected 验证服务器不可用时会快速返回明确错误，
// 不会假装上报成功。
func TestPublishWhileDisconnected(t *testing.T) {
	client := NewClient(testMQTTConfig(), []int{1})
	err := client.Publish(protocol.MeterData{MeterID: 1})
	if err == nil || !strings.Contains(err.Error(), "尚未建立") {
		t.Fatalf("期望未连接错误，实际：%v", err)
	}
}

// TestClientEnablesAutomaticReconnect 检查Paho客户端的断线重连配置。
func TestClientEnablesAutomaticReconnect(t *testing.T) {
	client := NewClient(testMQTTConfig(), []int{1})
	options := client.mqttClient.OptionsReader()
	if !options.AutoReconnect() {
		t.Fatal("MQTT客户端必须启用自动重连")
	}
}

// TestWaitPostReplyStopsWithClient 验证程序退出时，等待服务器回复的任务
// 会被stopped通道立即唤醒，而不是一直等到超时。
func TestWaitPostReplyStopsWithClient(t *testing.T) {
	client := NewClient(testMQTTConfig(), []int{1})
	client.stop()
	_, err := client.waitPostReply(1, make(chan postReply))
	if err == nil || !strings.Contains(err.Error(), "正在停止") {
		t.Fatalf("期望停止错误，实际：%v", err)
	}
}

func testMQTTConfig() config.MQTTConfig {
	return config.MQTTConfig{
		Broker: "tcp://127.0.0.1:1883", DeviceName: "dev1", Password: "123456",
		ReplyTimeout: time.Second, ChunkSize: 30, MaxRetransmits: 3,
	}
}
