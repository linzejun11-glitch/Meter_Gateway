package protocol

// CRC16CCITT 计算同事 MQTT 上传协议使用的 CRC16-CCITT。
//
// 算法参数：
//   - 初始值：0xFFFF
//   - 多项式：0x1021
//   - 最高位优先
//
// 这个算法只用于 Go 客户端与 Python MQTT Broker 之间的自定义帧。
// 电表侧的 Modbus-RTU 仍然使用 modbus.CRC16，二者不能混用。
func CRC16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)

	for _, b := range data {
		crc ^= uint16(b) << 8

		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}

	return crc
}
