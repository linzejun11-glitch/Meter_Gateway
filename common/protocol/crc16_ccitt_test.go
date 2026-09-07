package protocol

import "testing"

func TestCRC16CCITTKnownVector(t *testing.T) {
	// "123456789" 是 CRC 算法常用的标准检查文本。
	// CRC-16/CCITT-FALSE 对应结果应为 0x29B1。
	got := CRC16CCITT([]byte("123456789"))
	const want = uint16(0x29B1)

	if got != want {
		t.Fatalf("CRC16CCITT=%04X，期望%04X", got, want)
	}
}
