package driver

import (
	"strings"
	"testing"

	"MOCK_COLLECT/common/config"
)

// TestOpenRejectsUnknownDriver 只验证工厂分支，不会打开串口。
// 新品牌尚未注册时应尽早返回容易理解的配置错误。
func TestOpenRejectsUnknownDriver(t *testing.T) {
	_, err := Open(config.MeterConfig{Driver: "unknown_meter"})
	if err == nil {
		t.Fatal("未知驱动应返回错误")
	}
	if !strings.Contains(err.Error(), "unknown_meter") {
		t.Fatalf("错误信息应包含驱动名称，实际：%v", err)
	}
}
