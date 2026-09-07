package modbus

// CRC16 计算标准 Modbus CRC16。
//
// 算法参数：
//   - 初始值：0xFFFF
//   - 多项式：0xA001
//   - 逐字节、最低位优先
//
// Modbus-RTU 在线路上传输 CRC 时采用“低字节在前、高字节在后”。
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)

	for _, b := range data {
		crc ^= uint16(b)

		for bit := 0; bit < 8; bit++ {
			if crc&0x0001 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}

	return crc
}

// AppendCRC 在报文末尾追加两个 CRC 字节。
// 第一个是低字节，第二个是高字节。
func AppendCRC(frame []byte) []byte {
	crc := CRC16(frame)
	return append(frame, byte(crc), byte(crc>>8))
}

// VerifyCRC 检查一个完整 Modbus-RTU 报文的 CRC。
func VerifyCRC(frame []byte) bool {
	if len(frame) < 3 {
		return false
	}

	dataEnd := len(frame) - 2
	expected := CRC16(frame[:dataEnd])

	received := uint16(frame[dataEnd]) |
		uint16(frame[dataEnd+1])<<8

	return expected == received
}
