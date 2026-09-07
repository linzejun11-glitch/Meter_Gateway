package modbus

import (
	"errors"
	"testing"
)

// TestWaitForDigitalOutputStateAllowsDelayedUpdate 模拟地址310稍晚才更新。
//
// 前两次读取仍然是true，第三次才变成期望的false。
// 这正是现场第一次控制被旧代码误判失败的情况。
func TestWaitForDigitalOutputStateAllowsDelayedUpdate(t *testing.T) {
	values := []uint16{1, 1, 0}
	readCount := 0

	actual, known, err := waitForDigitalOutputState(
		func() (uint16, error) {
			value := values[readCount]
			readCount++
			return value, nil
		},
		0,
		false,
		len(values),
		0,
	)

	if err != nil {
		t.Fatalf("延迟更新最终成功时不应返回错误：%v", err)
	}
	if !known || actual {
		t.Fatalf("读回状态错误：actual=%t known=%t", actual, known)
	}
	if readCount != 3 {
		t.Fatalf("读取次数=%d，期望3", readCount)
	}
}

// TestWaitForDigitalOutputStateReturnsMismatch 验证超过重试次数后仍返回原错误类型。
func TestWaitForDigitalOutputStateReturnsMismatch(t *testing.T) {
	actual, known, err := waitForDigitalOutputState(
		func() (uint16, error) {
			return 1, nil
		},
		0,
		false,
		3,
		0,
	)

	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("期望ErrStateMismatch，实际：%v", err)
	}
	if !known || !actual {
		t.Fatalf("最后读回状态错误：actual=%t known=%t", actual, known)
	}
}

// TestWaitForDigitalOutputStateReturnsReadError 验证寄存器读取失败不会被误报为状态不一致。
func TestWaitForDigitalOutputStateReturnsReadError(t *testing.T) {
	readError := errors.New("simulated serial error")

	_, known, err := waitForDigitalOutputState(
		func() (uint16, error) {
			return 0, readError
		},
		0,
		false,
		3,
		0,
	)

	if !errors.Is(err, readError) {
		t.Fatalf("期望保留原读取错误，实际：%v", err)
	}
	if known {
		t.Fatal("一次状态都没有成功读取时known应为false")
	}
}
