package protocol

import "time"

const (
	MessageTypeMeterData     = "meter_data"
	MessageTypeSwitchStatus  = "switch_status"
	MessageTypeSwitchControl = "switch_control"
	MessageTypeControlACK    = "control_ack"
)

const timestampLayout = "2006-01-02 15:04:05"

// Envelope 只读取消息类型，用来决定下一步解析成哪个具体结构体。
type Envelope struct {
	MessageType string `json:"msg_type"`
}

// MeterData 是任务书约定的电表测量数据。
type MeterData struct {
	MessageType string  `json:"msg_type"`
	MeterID     int     `json:"meter_id"`
	Voltage     float64 `json:"voltage"`      // 单位：V
	Current     float64 `json:"current"`      // 单位：mA
	ActivePower float64 `json:"active_power"` // 单位：W
	Timestamp   string  `json:"timestamp"`
}

// SwitchChannels 表示业务侧看到的“第1路、第2路”。
//
// 对DO而言：
//   - Channel1 对应仪表 DO0、端子15/16、线圈地址0
//   - Channel2 对应仪表 DO1、端子17/18、线圈地址1
type SwitchChannels struct {
	Channel1 bool `json:"channel_1"`
	Channel2 bool `json:"channel_2"`
}

// SwitchStatus 是Go客户端向Python服务器上报的开关状态。
type SwitchStatus struct {
	MessageType    string         `json:"msg_type"`
	MeterID        int            `json:"meter_id"`
	DigitalInputs  SwitchChannels `json:"digital_inputs"`
	DigitalOutputs SwitchChannels `json:"digital_outputs"`
	Timestamp      string         `json:"timestamp"`
}

// SwitchControl 是Python服务器下发的DO控制命令。
//
// DesiredState 使用指针，是为了区分：
//   - 明确传入 false
//   - JSON中完全没有 desired_state 字段
type SwitchControl struct {
	MessageType  string `json:"msg_type"`
	RequestID    string `json:"request_id"`
	MeterID      int    `json:"meter_id"`
	Channel      int    `json:"channel"`
	DesiredState *bool  `json:"desired_state"`
	Timestamp    string `json:"timestamp"`
}

// ControlACK 是Go客户端返回给Python服务器的控制结果。
type ControlACK struct {
	MessageType  string `json:"msg_type"`
	RequestID    string `json:"request_id"`
	MeterID      int    `json:"meter_id"`
	Channel      int    `json:"channel"`
	DesiredState bool   `json:"desired_state"`

	// 如果写入后未能读回状态，ActualState会被省略。
	ActualState *bool `json:"actual_state,omitempty"`

	Success   bool   `json:"success"`
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// FormatTimestamp 统一客户端消息中的时间格式。
func FormatTimestamp(t time.Time) string {
	return t.Format(timestampLayout)
}
