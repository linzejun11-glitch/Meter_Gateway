package modbus

import "testing"

func TestCRC16KnownModbusRequest(t *testing.T) {
	// 标准示例：
	// 01 03 00 00 00 0A 的CRC在线路上应为 C5 CD。
	data := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A}
	frame := AppendCRC(data)

	if frame[len(frame)-2] != 0xC5 || frame[len(frame)-1] != 0xCD {
		t.Fatalf(
			"unexpected CRC bytes: got=%02X %02X want=C5 CD",
			frame[len(frame)-2],
			frame[len(frame)-1],
		)
	}

	if !VerifyCRC(frame) {
		t.Fatal("VerifyCRC rejected a valid frame")
	}
}

func TestVerifyCRCRejectsChangedData(t *testing.T) {
	frame := AppendCRC([]byte{0x01, 0x03, 0x00, 0x46, 0x00, 0x01})
	frame[3] ^= 0x01

	if VerifyCRC(frame) {
		t.Fatal("VerifyCRC accepted a damaged frame")
	}
}
