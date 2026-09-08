package controller

import (
	"context"
	"errors"
	"testing"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/device"
)

// silentClient 满足控制器的通信接口，但本测试只调用execute，
// 所以它不连接服务器，也不真正发送消息。
type silentClient struct {
	ready    chan struct{}
	commands chan protocol.SwitchControl
}

func newSilentClient() *silentClient {
	ready := make(chan struct{})
	close(ready)
	return &silentClient{
		ready:    ready,
		commands: make(chan protocol.SwitchControl),
	}
}

func (c *silentClient) Run(context.Context) error               { return nil }
func (c *silentClient) Ready() <-chan struct{}                  { return c.ready }
func (c *silentClient) Publish(any) error                       { return nil }
func (c *silentClient) Commands() <-chan protocol.SwitchControl { return c.commands }

// TestExecuteUsesDeviceInterface 验证控制器会把业务命令原样交给统一电表接口。
func TestExecuteUsesDeviceInterface(t *testing.T) {
	desired := true
	meter := &device.Fake{
		ControlActualState: true,
		ControlStateKnown:  true,
	}
	controller := New(meter, newSilentClient(), 1)

	ack := controller.execute(protocol.SwitchControl{
		RequestID:    "0000000000001",
		MeterID:      1,
		Channel:      2,
		DesiredState: &desired,
	})

	if !ack.Success || ack.ActualState == nil || !*ack.ActualState {
		t.Fatalf("控制ACK错误：%+v", ack)
	}
	if len(meter.ControlCalls) != 1 || meter.ControlCalls[0].Channel != 2 || !meter.ControlCalls[0].State {
		t.Fatalf("电表控制调用错误：%+v", meter.ControlCalls)
	}
}

// TestExecuteMapsUnifiedDeviceError 验证任何品牌驱动只要返回统一错误，
// 控制器就能生成一致的业务错误码。
func TestExecuteMapsUnifiedDeviceError(t *testing.T) {
	desired := false
	meter := &device.Fake{
		ControlActualState: true,
		ControlStateKnown:  true,
		ControlError:       errors.Join(device.ErrStateMismatch, errors.New("simulated")),
	}
	controller := New(meter, newSilentClient(), 1)

	ack := controller.execute(protocol.SwitchControl{
		RequestID:    "0000000000002",
		MeterID:      1,
		Channel:      1,
		DesiredState: &desired,
	})

	if ack.Success || ack.ErrorCode != "STATE_MISMATCH" {
		t.Fatalf("错误ACK映射错误：%+v", ack)
	}
	if ack.ActualState == nil || !*ack.ActualState {
		t.Fatalf("已知的实际状态应保留在ACK中：%+v", ack)
	}
}

// TestControlErrorCodeKeepsServerContract 验证内部改成统一错误后，
// 发给现有服务器的错误码仍保持兼容。
func TestControlErrorCodeKeepsServerContract(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: device.ErrTimeout, want: "MODBUS_TIMEOUT"},
		{err: device.ErrDeviceException, want: "MODBUS_EXCEPTION"},
		{err: device.ErrChecksum, want: "MODBUS_CRC_ERROR"},
		{err: device.ErrProtocol, want: "MODBUS_ERROR"},
		{err: device.ErrStateMismatch, want: "STATE_MISMATCH"},
	}

	for _, test := range tests {
		if got := controlErrorCode(test.err); got != test.want {
			t.Fatalf("controlErrorCode(%v)=%q，期望%q", test.err, got, test.want)
		}
	}
}
