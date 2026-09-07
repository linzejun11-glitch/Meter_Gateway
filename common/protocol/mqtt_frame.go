package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// MQTTFrameHeaderSize 是同事自定义上传帧的固定头部长度：
	// 帧头(2) + 会话ID(2) + 包序号(2) + 总包数(2) + 长度(2) + CRC(2)。
	MQTTFrameHeaderSize = 12

	MQTTFrameSingle = uint16(0xAABB)
	MQTTFrameFirst  = uint16(0xAACC)
	MQTTFrameMiddle = uint16(0xAADD)
	MQTTFrameLast   = uint16(0xAAEE)
)

// EncodeMQTTSingleFrame 把一条完整JSON封装成同事协议的单帧。
//
//	AA BB | session_id | idx=0 | total=1 | length | CRC16-CCITT | JSON
//
// 所有uint16字段都使用大端序，CRC只覆盖JSON载荷。
func EncodeMQTTSingleFrame(sessionID uint16, payload []byte) ([]byte, error) {
	return EncodeMQTTFrame(MQTTFrameSingle, sessionID, 0, 1, payload)
}

// EncodeMQTTFrame 封装一片MQTT上报数据。
//
//	frame_type | session_id | idx | total | length | CRC16-CCITT | payload
//
// 所有uint16字段都使用大端序，CRC只覆盖当前片的payload。
func EncodeMQTTFrame(
	frameType uint16,
	sessionID uint16,
	index uint16,
	total uint16,
	payload []byte,
) ([]byte, error) {
	if !validMQTTFrameType(frameType) {
		return nil, fmt.Errorf("unsupported MQTT frame type: 0x%04X", frameType)
	}
	if total == 0 {
		return nil, errors.New("MQTT frame total cannot be zero")
	}
	if index >= total {
		return nil, fmt.Errorf("MQTT frame index %d must be less than total %d", index, total)
	}
	if len(payload) == 0 {
		return nil, errors.New("MQTT frame payload cannot be empty")
	}
	if len(payload) > int(^uint16(0)) {
		return nil, fmt.Errorf("MQTT frame payload too large: %d", len(payload))
	}

	frame := make([]byte, MQTTFrameHeaderSize+len(payload))
	binary.BigEndian.PutUint16(frame[0:2], frameType)
	binary.BigEndian.PutUint16(frame[2:4], sessionID)
	binary.BigEndian.PutUint16(frame[4:6], index)
	binary.BigEndian.PutUint16(frame[6:8], total)
	binary.BigEndian.PutUint16(frame[8:10], uint16(len(payload)))
	binary.BigEndian.PutUint16(frame[10:12], CRC16CCITT(payload))
	copy(frame[MQTTFrameHeaderSize:], payload)

	return frame, nil
}

func validMQTTFrameType(frameType uint16) bool {
	switch frameType {
	case MQTTFrameSingle, MQTTFrameFirst, MQTTFrameMiddle, MQTTFrameLast:
		return true
	default:
		return false
	}
}
