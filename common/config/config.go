package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// TransportType 表示程序使用哪一种网络通信方式。
//
// transport 的中文含义是“传输方式”。
// 使用自定义字符串类型而不是直接使用 string，可以避免代码中到处出现
// "mqtt" 这类容易拼错的裸字符串。
type TransportType string

const (
	// TransportMQTT 表示连接同事开发的 Python MQTT Broker。
	TransportMQTT TransportType = "mqtt"
)

// MeterConfig 保存电表串口和 Modbus 从站配置。
//
// 注意：这里只描述“如何找到电表并和它说话”，不包含寄存器地址。
// 寄存器地址属于设备协议，统一放在 modbus 包中。
type MeterConfig struct {
	// PortName 是 Windows 设备管理器显示的虚拟串口名称。
	PortName string

	// BaudRate、DataBits、Parity、StopBits 必须与仪表菜单设置完全一致。
	BaudRate int
	DataBits int
	Parity   string
	StopBits int

	// SlaveID 是仪表的 Modbus 地址，合法范围通常为 1~247。
	SlaveID byte

	// Timeout 是每次 Modbus 请求等待仪表响应的最长时间。
	Timeout time.Duration
}

// MQTTConfig 保存同事开发的 Python MQTT Broker 连接参数。
type MQTTConfig struct {
	// Broker 使用“协议://主机:端口”格式。
	// 同机联调示例：tcp://127.0.0.1:1883。
	Broker string

	// DeviceName 同时用作 MQTT Client ID 和 Username。
	DeviceName string

	// Password 是 Python Broker 中为设备配置的认证密钥。
	Password string

	// ReplyTimeout 是上报后等待服务端 cmd 回复的最长时间。
	ReplyTimeout time.Duration

	// ChunkSize 是多帧上报时每个数据片段的最大字节数。
	ChunkSize int

	// MaxRetransmits 是客户端响应cmd=2或cmd=3的最多重传轮数。
	MaxRetransmits int
}

// AppConfig 是整个程序的总配置。
type AppConfig struct {
	// Transport 当前固定为 Python MQTT。
	Transport TransportType

	Meter MeterConfig
	MQTT  MQTTConfig

	// CollectInterval 是电表轮询周期。
	CollectInterval time.Duration
}

// LoadAppConfig 从环境变量和默认值中构造程序配置。
//
// 当前支持 TRANSPORT=mqtt，连接同事开发的 Python MQTT Broker。
//
// 如果没有设置 TRANSPORT，默认使用 mqtt。
func LoadAppConfig() (AppConfig, error) {
	transport, err := ParseTransport(os.Getenv("TRANSPORT"))
	if err != nil {
		return AppConfig{}, err
	}

	cfg := AppConfig{
		Transport: transport,
		Meter: MeterConfig{
			PortName: "COM3",
			BaudRate: 9600,
			DataBits: 8,
			Parity:   "N",
			StopBits: 1,
			SlaveID:  1,
			Timeout:  3 * time.Second,
		},
		MQTT: MQTTConfig{
			Broker:         envOrDefault("MQTT_BROKER", "tcp://192.168.54.150:1883"),
			DeviceName:     envOrDefault("MQTT_DEVICE_NAME", "dev1"),
			Password:       envOrDefault("MQTT_PASSWORD", "123456"),
			ReplyTimeout:   10 * time.Second,
			ChunkSize:      30,
			MaxRetransmits: 3,
		},
		CollectInterval: 8 * time.Second,
	}

	if err := cfg.Validate(); err != nil {
		return AppConfig{}, err
	}

	return cfg, nil
}

// ParseTransport 把环境变量中的文本转换为 TransportType。
//
// TrimSpace 会去掉首尾空格，ToLower 允许用户填写 MQTT、Mqtt 或 mqtt。
func ParseTransport(value string) (TransportType, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))

	switch normalized {
	case "", string(TransportMQTT):
		return TransportMQTT, nil
	default:
		return "", fmt.Errorf(
			"不支持的通信方式 %q：TRANSPORT只能是 mqtt",
			value,
		)
	}
}

// Validate 检查配置中会导致程序无法正常运行的明显错误。
func (cfg AppConfig) Validate() error {
	if cfg.Meter.PortName == "" {
		return fmt.Errorf("串口名称不能为空")
	}
	if cfg.Meter.SlaveID == 0 {
		return fmt.Errorf("Modbus Slave ID不能为0")
	}
	if cfg.Meter.Timeout <= 0 {
		return fmt.Errorf("Modbus超时时间必须大于0")
	}
	if cfg.CollectInterval <= 0 {
		return fmt.Errorf("采集周期必须大于0")
	}

	switch cfg.Transport {
	case TransportMQTT:
		if strings.TrimSpace(cfg.MQTT.Broker) == "" {
			return fmt.Errorf("Python MQTT Broker地址为空，请设置MQTT_BROKER")
		}
		if strings.TrimSpace(cfg.MQTT.DeviceName) == "" {
			return fmt.Errorf("MQTT设备名称为空，请设置MQTT_DEVICE_NAME")
		}
		if strings.TrimSpace(cfg.MQTT.Password) == "" {
			return fmt.Errorf("MQTT设备密码为空，请设置MQTT_PASSWORD")
		}
		if cfg.MQTT.ReplyTimeout <= 0 {
			return fmt.Errorf("MQTT上报回复超时时间必须大于0")
		}
		if cfg.MQTT.ChunkSize <= 0 {
			return fmt.Errorf("MQTT分片大小必须大于0")
		}
		if cfg.MQTT.ChunkSize > 65535 {
			return fmt.Errorf("MQTT分片大小不能超过65535字节")
		}
		if cfg.MQTT.MaxRetransmits <= 0 {
			return fmt.Errorf("MQTT最大重传轮数必须大于0")
		}

	default:
		return fmt.Errorf("不支持的通信方式 %q", cfg.Transport)
	}

	return nil
}

// envOrDefault 返回环境变量值；变量未设置时返回默认值。
func envOrDefault(name, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}
	return value
}
