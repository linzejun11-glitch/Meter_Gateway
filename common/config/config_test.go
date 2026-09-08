package config

import (
	"testing"
	"time"
)

// TestParseTransport 验证环境变量文本可以被稳定地转换成通信方式。
func TestParseTransport(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      TransportType
		wantError bool
	}{
		{
			name:  "未设置时默认MQTT",
			input: "",
			want:  TransportMQTT,
		},
		{
			name:  "显式选择MQTT",
			input: "mqtt",
			want:  TransportMQTT,
		},
		{
			name:  "MQTT允许大小写和首尾空格",
			input: "  MQTT  ",
			want:  TransportMQTT,
		},
		{
			name:      "拒绝未知通信方式",
			input:     "http",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseTransport(test.input)

			if test.wantError {
				if err == nil {
					t.Fatalf("ParseTransport(%q)应返回错误", test.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseTransport(%q)返回意外错误：%v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("ParseTransport(%q)=%q，期望%q", test.input, got, test.want)
			}
		})
	}
}

// TestLoadAppConfigForMQTT 验证默认值和环境变量可以构造MQTT配置。
func TestLoadAppConfigForMQTT(t *testing.T) {
	t.Setenv("TRANSPORT", "mqtt")
	// 设为空字符串，用来稳定验证未选择驱动时会采用qs300默认值。
	t.Setenv("METER_DRIVER", "")
	t.Setenv("MQTT_BROKER", "tcp://192.0.2.10:1883")
	t.Setenv("MQTT_DEVICE_NAME", "meter-gateway-1")
	t.Setenv("MQTT_PASSWORD", "test-secret")
	cfg, err := LoadAppConfig()
	if err != nil {
		t.Fatalf("加载MQTT配置失败：%v", err)
	}
	if cfg.Transport != TransportMQTT {
		t.Fatalf("通信方式=%q，期望%q", cfg.Transport, TransportMQTT)
	}
	if cfg.MQTT.Broker != "tcp://192.0.2.10:1883" {
		t.Fatalf("MQTT Broker=%q", cfg.MQTT.Broker)
	}
	if cfg.MQTT.DeviceName != "meter-gateway-1" {
		t.Fatalf("MQTT设备名称=%q", cfg.MQTT.DeviceName)
	}
	if cfg.Meter.Driver != "qs300" {
		t.Fatalf("电表驱动=%q，期望qs300", cfg.Meter.Driver)
	}
}

// validTestConfig 返回一份不依赖真实串口和网络的合法配置。
func validTestConfig() AppConfig {
	return AppConfig{
		Transport: TransportMQTT,
		Meter: MeterConfig{
			Driver:   "qs300",
			PortName: "COM_TEST",
			SlaveID:  1,
			Timeout:  time.Second,
		},
		MQTT: MQTTConfig{
			Broker:         "tcp://example.invalid:1883",
			DeviceName:     "device",
			Password:       "test-password",
			ReplyTimeout:   time.Second,
			ChunkSize:      30,
			MaxRetransmits: 3,
		},
		CollectInterval: time.Second,
	}
}

// TestValidateRejectsOversizedMQTTChunk 验证单片数据不会超过协议中
// 2字节length字段能表示的最大值（65535）。
func TestValidateRejectsOversizedMQTTChunk(t *testing.T) {
	cfg := validTestConfig()
	cfg.MQTT.ChunkSize = 65536

	if err := cfg.Validate(); err == nil {
		t.Fatal("MQTT分片大小超过65535时应返回错误")
	}
}
