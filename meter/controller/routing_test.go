package controller

import (
	"testing"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/device"
)

// TestExecuteRoutesToTargetMeter 验证同一个控制器会根据meter_id选择电表，
// 而不会把命令交给共享RS485总线上的其他设备。
func TestExecuteRoutesToTargetMeter(t *testing.T) {
	desired := true
	first := &device.Fake{}
	second := &device.Fake{
		ControlActualState: true,
		ControlStateKnown:  true,
	}
	controller := New(
		map[int]device.Device{101: first, 102: second},
		newSilentClient(),
	)

	ack := controller.execute(protocol.SwitchControl{
		RequestID:    "0000000000102",
		MeterID:      102,
		Channel:      1,
		DesiredState: &desired,
	})
	if !ack.Success {
		t.Fatalf("控制应成功：%+v", ack)
	}
	if len(first.ControlCalls) != 0 {
		t.Fatalf("meter101不应收到命令：%+v", first.ControlCalls)
	}
	if len(second.ControlCalls) != 1 {
		t.Fatalf("meter102应收到一次命令：%+v", second.ControlCalls)
	}
}
