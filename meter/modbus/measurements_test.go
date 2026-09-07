package modbus

import "testing"

// TestMeasurementsFromRawUsesMilliamps 验证QS300的0.001A电流原始值
// 会按照双方约定转换成mA后再交给采集和上传模块。
func TestMeasurementsFromRawUsesMilliamps(t *testing.T) {
	got := measurementsFromRaw(2205, 1250, 275)

	if got.Voltage != 220.5 {
		t.Fatalf("voltage=%v，期望220.5V", got.Voltage)
	}
	if got.Current != 1250 {
		t.Fatalf("current=%v，期望1250mA", got.Current)
	}
	if got.ActivePower != 275 {
		t.Fatalf("active_power=%v，期望275W", got.ActivePower)
	}
}
