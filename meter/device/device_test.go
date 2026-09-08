package device

import (
	"errors"
	"testing"
)

// TestFakeRecordsControlAndClose 验证通用模拟电表能记录上层行为。
// 后续新增业务模块时，可以继续复用Fake而不需要制作串口测试设备。
func TestFakeRecordsControlAndClose(t *testing.T) {
	fake := &Fake{
		ControlActualState: true,
		ControlStateKnown:  true,
		ControlError:       ErrStateMismatch,
	}

	actual, known, err := fake.ControlDigitalOutput(2, false)
	if !actual || !known || !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("模拟控制返回值错误：actual=%t known=%t err=%v", actual, known, err)
	}
	if len(fake.ControlCalls) != 1 || fake.ControlCalls[0].Channel != 2 || fake.ControlCalls[0].State {
		t.Fatalf("模拟控制记录错误：%+v", fake.ControlCalls)
	}

	if err := fake.Close(); err != nil {
		t.Fatalf("Fake.Close返回意外错误：%v", err)
	}
	if !fake.Closed {
		t.Fatal("Fake应记录已关闭状态")
	}
}
