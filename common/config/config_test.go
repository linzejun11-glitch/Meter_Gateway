package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
transport: mqtt
rs485:
  port: COM_TEST
  baud_rate: 9600
  data_bits: 8
  parity: N
  stop_bits: 1
  timeout: 3s
meters:
  - meter_id: 101
    name: meter001
    driver: qs300
    slave_id: 1
    collect_interval: 8s
  - meter_id: 102
    name: meter002
    driver: qs300
    slave_id: 2
    collect_interval: 10s
mqtt:
  broker: tcp://example.invalid:1883
  device_name: gateway-test
  password: test-password
  reply_timeout: 10s
  chunk_size: 30
  max_retransmits: 3
`

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	clearConfigOverrides(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func clearConfigOverrides(t *testing.T) {
	t.Helper()
	// 防止开发者终端中遗留的部署环境变量影响单元测试结果。
	for _, name := range []string{
		"TRANSPORT", "MQTT_BROKER", "MQTT_DEVICE_NAME", "MQTT_PASSWORD",
	} {
		t.Setenv(name, "")
	}
}

// TestLoadAppConfigFromFile 验证多电表YAML会转换成强类型配置。
func TestLoadAppConfigFromFile(t *testing.T) {
	cfg, err := LoadAppConfigFromFile(writeTestConfig(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RS485.PortName != "COM_TEST" || cfg.RS485.Timeout != 3*time.Second {
		t.Fatalf("RS485配置错误：%+v", cfg.RS485)
	}
	if len(cfg.Meters) != 2 || cfg.Meters[1].MeterID != 102 ||
		cfg.Meters[1].SlaveID != 2 || cfg.Meters[1].CollectInterval != 10*time.Second {
		t.Fatalf("电表配置错误：%+v", cfg.Meters)
	}
	if got := cfg.MeterIDs(); len(got) != 2 || got[0] != 101 || got[1] != 102 {
		t.Fatalf("MeterIDs=%v", got)
	}
}

// TestLoadAppConfigRejectsUnknownField 验证拼错YAML字段时程序会直接报错。
func TestLoadAppConfigRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(validYAML, "slave_id: 1", "salve_id: 1", 1)
	_, err := LoadAppConfigFromFile(writeTestConfig(t, bad))
	if err == nil || !strings.Contains(err.Error(), "salve_id") {
		t.Fatalf("期望未知字段错误，实际：%v", err)
	}
}

// TestValidateRejectsDuplicateIDs 验证业务编号和从站地址都不能重复。
func TestValidateRejectsDuplicateIDs(t *testing.T) {
	cfg, err := LoadAppConfigFromFile(writeTestConfig(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Meters[1].SlaveID = cfg.Meters[0].SlaveID
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "slave_id") {
		t.Fatalf("期望重复Slave ID错误，实际：%v", err)
	}

	cfg.Meters[1].SlaveID = 2
	cfg.Meters[1].MeterID = cfg.Meters[0].MeterID
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "meter_id") {
		t.Fatalf("期望重复Meter ID错误，实际：%v", err)
	}
}

// TestEnvironmentOverridesMQTT 验证部署时可以不修改YAML而覆盖网络参数。
func TestEnvironmentOverridesMQTT(t *testing.T) {
	path := writeTestConfig(t, validYAML)
	t.Setenv("MQTT_BROKER", "tcp://192.0.2.10:1883")
	t.Setenv("MQTT_DEVICE_NAME", "gateway-from-env")
	t.Setenv("MQTT_PASSWORD", "password-from-env")
	cfg, err := LoadAppConfigFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MQTT.Broker != "tcp://192.0.2.10:1883" ||
		cfg.MQTT.DeviceName != "gateway-from-env" ||
		cfg.MQTT.Password != "password-from-env" {
		t.Fatalf("环境变量覆盖失败：%+v", cfg.MQTT)
	}
}

func TestParseTransport(t *testing.T) {
	if got, err := ParseTransport(" MQTT "); err != nil || got != TransportMQTT {
		t.Fatalf("ParseTransport返回：%q %v", got, err)
	}
	if _, err := ParseTransport("http"); err == nil {
		t.Fatal("未知传输方式应返回错误")
	}
}

// TestRepositoryConfigIsValid防止以后修改示例YAML时拼错字段。
// 这里只解析文件，不会打开串口或连接MQTT。
func TestRepositoryConfigIsValid(t *testing.T) {
	clearConfigOverrides(t)
	path := filepath.Join("..", "..", "config.yaml")
	if _, err := LoadAppConfigFromFile(path); err != nil {
		t.Fatalf("仓库中的config.yaml无效：%v", err)
	}
}
