// Package config 负责从YAML文件和环境变量读取网关配置。
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config.yaml"

// TransportType 表示网关与服务器之间采用的传输方式。
type TransportType string

const (
	// TransportMQTT 表示连接同事开发的Python MQTT Broker。
	TransportMQTT TransportType = "mqtt"
)

// RS485Config 是所有电表共用的一条RS485总线配置。
//
// 当前项目明确只支持“一个串口挂多块电表”。因此串口参数只配置一次，
// 每块电表再使用不同的Slave ID区分。
type RS485Config struct {
	PortName string
	BaudRate int
	DataBits int
	Parity   string
	StopBits int
	Timeout  time.Duration
}

// MeterConfig 描述RS485总线上的一块电表。
type MeterConfig struct {
	// MeterID 是服务器和MQTT消息使用的业务编号。
	// 它与SlaveID分开，避免以后修改现场地址时影响服务器中的设备编号。
	MeterID int

	// Name 只用于日志和人工识别，例如meter001。
	Name string

	// Driver 决定使用哪个型号驱动，目前支持qs300。
	Driver string

	// SlaveID 是这块电表在共享RS485总线上的Modbus从站地址。
	SlaveID byte

	// CollectInterval 是这块电表自己的采集周期。
	CollectInterval time.Duration
}

// MQTTConfig 保存整个网关唯一的一条MQTT连接配置。
// 所有电表的数据都通过这条连接发送，并使用meter_id区分来源。
type MQTTConfig struct {
	Broker         string
	DeviceName     string
	Password       string
	ReplyTimeout   time.Duration
	ChunkSize      int
	MaxRetransmits int
}

// AppConfig 是程序完成校验后使用的内存配置。
type AppConfig struct {
	Transport TransportType
	RS485     RS485Config
	Meters    []MeterConfig
	MQTT      MQTTConfig
}

// 以下yaml*结构体只负责接收配置文件中的文本。
// 持续时间先读取成"3s"、"8s"等字符串，再统一转换成time.Duration。
type yamlConfig struct {
	Transport string          `yaml:"transport"`
	RS485     yamlRS485Config `yaml:"rs485"`
	Meters    []yamlMeter     `yaml:"meters"`
	MQTT      yamlMQTTConfig  `yaml:"mqtt"`
}

type yamlRS485Config struct {
	PortName string `yaml:"port"`
	BaudRate int    `yaml:"baud_rate"`
	DataBits int    `yaml:"data_bits"`
	Parity   string `yaml:"parity"`
	StopBits int    `yaml:"stop_bits"`
	Timeout  string `yaml:"timeout"`
}

type yamlMeter struct {
	MeterID         int    `yaml:"meter_id"`
	Name            string `yaml:"name"`
	Driver          string `yaml:"driver"`
	SlaveID         byte   `yaml:"slave_id"`
	CollectInterval string `yaml:"collect_interval"`
}

type yamlMQTTConfig struct {
	Broker         string `yaml:"broker"`
	DeviceName     string `yaml:"device_name"`
	Password       string `yaml:"password"`
	ReplyTimeout   string `yaml:"reply_timeout"`
	ChunkSize      int    `yaml:"chunk_size"`
	MaxRetransmits int    `yaml:"max_retransmits"`
}

// LoadAppConfig 读取网关配置。
//
// 默认读取当前目录的config.yaml。设置GATEWAY_CONFIG后可以指定其他文件：
//
//	$env:GATEWAY_CONFIG="C:\\gateway\\production.yaml"
func LoadAppConfig() (AppConfig, error) {
	path := strings.TrimSpace(os.Getenv("GATEWAY_CONFIG"))
	if path == "" {
		path = defaultConfigPath
	}
	return LoadAppConfigFromFile(path)
}

// LoadAppConfigFromFile 读取一个明确路径，主要供测试和工具复用。
func LoadAppConfigFromFile(path string) (AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, fmt.Errorf("读取配置文件%q失败: %w", path, err)
	}

	var raw yamlConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	// KnownFields会拒绝拼错的字段，例如把slave_id误写成salve_id。
	// 若不启用，YAML库会静默忽略拼错字段，排查现场问题会很困难。
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return AppConfig{}, fmt.Errorf("解析配置文件%q失败: %w", path, err)
	}

	cfg, err := raw.toAppConfig()
	if err != nil {
		return AppConfig{}, fmt.Errorf("配置文件%q无效: %w", path, err)
	}
	applyEnvironmentOverrides(&cfg)
	if err := cfg.Validate(); err != nil {
		return AppConfig{}, fmt.Errorf("配置文件%q校验失败: %w", path, err)
	}
	return cfg, nil
}

// toAppConfig 把YAML文本转换成业务代码需要的强类型配置。
func (raw yamlConfig) toAppConfig() (AppConfig, error) {
	transport, err := ParseTransport(raw.Transport)
	if err != nil {
		return AppConfig{}, err
	}
	rtuTimeout, err := parseDuration("rs485.timeout", raw.RS485.Timeout)
	if err != nil {
		return AppConfig{}, err
	}
	replyTimeout, err := parseDuration("mqtt.reply_timeout", raw.MQTT.ReplyTimeout)
	if err != nil {
		return AppConfig{}, err
	}

	meters := make([]MeterConfig, 0, len(raw.Meters))
	for index, item := range raw.Meters {
		interval, parseErr := parseDuration(
			fmt.Sprintf("meters[%d].collect_interval", index),
			item.CollectInterval,
		)
		if parseErr != nil {
			return AppConfig{}, parseErr
		}
		meters = append(meters, MeterConfig{
			MeterID:         item.MeterID,
			Name:            strings.TrimSpace(item.Name),
			Driver:          strings.ToLower(strings.TrimSpace(item.Driver)),
			SlaveID:         item.SlaveID,
			CollectInterval: interval,
		})
	}

	return AppConfig{
		Transport: transport,
		RS485: RS485Config{
			PortName: strings.TrimSpace(raw.RS485.PortName),
			BaudRate: raw.RS485.BaudRate,
			DataBits: raw.RS485.DataBits,
			Parity:   strings.ToUpper(strings.TrimSpace(raw.RS485.Parity)),
			StopBits: raw.RS485.StopBits,
			Timeout:  rtuTimeout,
		},
		Meters: meters,
		MQTT: MQTTConfig{
			Broker:         strings.TrimSpace(raw.MQTT.Broker),
			DeviceName:     strings.TrimSpace(raw.MQTT.DeviceName),
			Password:       raw.MQTT.Password,
			ReplyTimeout:   replyTimeout,
			ChunkSize:      raw.MQTT.ChunkSize,
			MaxRetransmits: raw.MQTT.MaxRetransmits,
		},
	}, nil
}

// applyEnvironmentOverrides 只覆盖经常因部署环境变化的网络参数。
// 空环境变量表示“不覆盖YAML”。
func applyEnvironmentOverrides(cfg *AppConfig) {
	if value := strings.TrimSpace(os.Getenv("TRANSPORT")); value != "" {
		if transport, err := ParseTransport(value); err == nil {
			cfg.Transport = transport
		} else {
			// 保存非法值，让Validate返回统一的不支持错误。
			cfg.Transport = TransportType(value)
		}
	}
	if value := strings.TrimSpace(os.Getenv("MQTT_BROKER")); value != "" {
		cfg.MQTT.Broker = value
	}
	if value := strings.TrimSpace(os.Getenv("MQTT_DEVICE_NAME")); value != "" {
		cfg.MQTT.DeviceName = value
	}
	if value := os.Getenv("MQTT_PASSWORD"); strings.TrimSpace(value) != "" {
		cfg.MQTT.Password = value
	}
}

// ParseTransport 把配置文本转换为TransportType。
func ParseTransport(value string) (TransportType, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = string(TransportMQTT)
	}
	if normalized != string(TransportMQTT) {
		return "", fmt.Errorf("不支持的通信方式%q：当前只能是mqtt", value)
	}
	return TransportMQTT, nil
}

func parseDuration(field, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s=%q不是合法时长: %w", field, value, err)
	}
	return duration, nil
}

// Validate 在打开串口或连接服务器之前检查全部配置。
func (cfg AppConfig) Validate() error {
	if cfg.Transport != TransportMQTT {
		return fmt.Errorf("不支持的通信方式%q", cfg.Transport)
	}
	if cfg.RS485.PortName == "" {
		return fmt.Errorf("rs485.port不能为空")
	}
	if cfg.RS485.BaudRate <= 0 {
		return fmt.Errorf("rs485.baud_rate必须大于0")
	}
	if cfg.RS485.DataBits < 5 || cfg.RS485.DataBits > 8 {
		return fmt.Errorf("rs485.data_bits必须在5到8之间")
	}
	if cfg.RS485.Parity != "N" && cfg.RS485.Parity != "E" && cfg.RS485.Parity != "O" {
		return fmt.Errorf("rs485.parity只能是N、E或O")
	}
	if cfg.RS485.StopBits != 1 && cfg.RS485.StopBits != 2 {
		return fmt.Errorf("rs485.stop_bits只能是1或2")
	}
	if cfg.RS485.Timeout <= 0 {
		return fmt.Errorf("rs485.timeout必须大于0")
	}
	if len(cfg.Meters) == 0 {
		return fmt.Errorf("meters至少需要配置一块电表")
	}

	meterIDs := make(map[int]struct{}, len(cfg.Meters))
	slaveIDs := make(map[byte]struct{}, len(cfg.Meters))
	names := make(map[string]struct{}, len(cfg.Meters))
	for index, meter := range cfg.Meters {
		prefix := fmt.Sprintf("meters[%d]", index)
		if meter.MeterID <= 0 {
			return fmt.Errorf("%s.meter_id必须大于0", prefix)
		}
		if _, exists := meterIDs[meter.MeterID]; exists {
			return fmt.Errorf("meter_id=%d重复", meter.MeterID)
		}
		meterIDs[meter.MeterID] = struct{}{}
		if meter.Name == "" {
			return fmt.Errorf("%s.name不能为空", prefix)
		}
		if _, exists := names[meter.Name]; exists {
			return fmt.Errorf("电表名称%q重复", meter.Name)
		}
		names[meter.Name] = struct{}{}
		if meter.Driver == "" {
			return fmt.Errorf("%s.driver不能为空", prefix)
		}
		if meter.SlaveID == 0 || meter.SlaveID > 247 {
			return fmt.Errorf("%s.slave_id必须在1到247之间", prefix)
		}
		if _, exists := slaveIDs[meter.SlaveID]; exists {
			return fmt.Errorf("共享RS485总线上的slave_id=%d重复", meter.SlaveID)
		}
		slaveIDs[meter.SlaveID] = struct{}{}
		if meter.CollectInterval <= 0 {
			return fmt.Errorf("%s.collect_interval必须大于0", prefix)
		}
	}

	if cfg.MQTT.Broker == "" {
		return fmt.Errorf("mqtt.broker不能为空")
	}
	if cfg.MQTT.DeviceName == "" {
		return fmt.Errorf("mqtt.device_name不能为空")
	}
	if strings.TrimSpace(cfg.MQTT.Password) == "" {
		return fmt.Errorf("mqtt.password不能为空")
	}
	if cfg.MQTT.ReplyTimeout <= 0 {
		return fmt.Errorf("mqtt.reply_timeout必须大于0")
	}
	if cfg.MQTT.ChunkSize <= 0 || cfg.MQTT.ChunkSize > 65535 {
		return fmt.Errorf("mqtt.chunk_size必须在1到65535之间")
	}
	if cfg.MQTT.MaxRetransmits <= 0 {
		return fmt.Errorf("mqtt.max_retransmits必须大于0")
	}
	return nil
}

// MeterIDs 返回MQTT控制路由需要的全部业务电表编号。
func (cfg AppConfig) MeterIDs() []int {
	ids := make([]int, 0, len(cfg.Meters))
	for _, meter := range cfg.Meters {
		ids = append(ids, meter.MeterID)
	}
	return ids
}
