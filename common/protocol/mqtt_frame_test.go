package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeMQTTSingleFrame(t *testing.T) {
	payload := []byte(`{"id":7,"params":{"voltage":220.5}}`)

	frame, err := EncodeMQTTSingleFrame(7, payload)
	if err != nil {
		t.Fatal(err)
	}

	if frame[0] != 0xAA || frame[1] != 0xBB {
		t.Fatalf("帧头=%02X %02X，期望AA BB", frame[0], frame[1])
	}
	if got := binary.BigEndian.Uint16(frame[2:4]); got != 7 {
		t.Fatalf("session_id=%d，期望7", got)
	}
	if got := binary.BigEndian.Uint16(frame[4:6]); got != 0 {
		t.Fatalf("idx=%d，期望0", got)
	}
	if got := binary.BigEndian.Uint16(frame[6:8]); got != 1 {
		t.Fatalf("total=%d，期望1", got)
	}
	if got := binary.BigEndian.Uint16(frame[8:10]); got != uint16(len(payload)) {
		t.Fatalf("length=%d，期望%d", got, len(payload))
	}
	if got := binary.BigEndian.Uint16(frame[10:12]); got != CRC16CCITT(payload) {
		t.Fatalf("CRC=%04X，期望%04X", got, CRC16CCITT(payload))
	}
	if !bytes.Equal(frame[MQTTFrameHeaderSize:], payload) {
		t.Fatalf("payload=%q，期望%q", frame[MQTTFrameHeaderSize:], payload)
	}
}

func TestEncodeMQTTSingleFrameRejectsInvalidInput(t *testing.T) {
	frame, err := EncodeMQTTSingleFrame(0, []byte("{}"))
	if err != nil {
		t.Fatalf("协议允许session_id=0，不应返回错误：%v", err)
	}
	if got := binary.BigEndian.Uint16(frame[2:4]); got != 0 {
		t.Fatalf("session_id=%d，期望0", got)
	}
	if _, err := EncodeMQTTSingleFrame(1, nil); err == nil {
		t.Fatal("payload为空时应返回错误")
	}
}

func TestEncodeMQTTMultipartHeaders(t *testing.T) {
	tests := []struct {
		frameType uint16
		index     uint16
		total     uint16
	}{
		{frameType: MQTTFrameFirst, index: 0, total: 3},
		{frameType: MQTTFrameMiddle, index: 1, total: 3},
		{frameType: MQTTFrameLast, index: 2, total: 3},
	}

	for _, test := range tests {
		frame, err := EncodeMQTTFrame(
			test.frameType,
			5,
			test.index,
			test.total,
			[]byte("part"),
		)
		if err != nil {
			t.Fatal(err)
		}

		if got := binary.BigEndian.Uint16(frame[0:2]); got != test.frameType {
			t.Fatalf("frame type=%04X，期望%04X", got, test.frameType)
		}
	}
}
