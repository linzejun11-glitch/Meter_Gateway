package transport

import (
	"testing"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/mqttclient"
)

// TestNewClient 验证工厂会按照配置创建正确的客户端类型。
//
// 这里只创建对象，不会连接真实Python服务器。
func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		transport config.TransportType
		wantType  string
	}{
		{
			name:      "创建Python MQTT客户端",
			transport: config.TransportMQTT,
			wantType:  "mqtt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.AppConfig{
				Transport: test.transport,
				MQTT: config.MQTTConfig{
					Broker:         "tcp://example.invalid:1883",
					DeviceName:     "device",
					Password:       "password",
					ChunkSize:      30,
					MaxRetransmits: 3,
				},
			}

			client, err := NewClient(cfg)
			if err != nil {
				t.Fatalf("NewClient返回意外错误：%v", err)
			}

			switch test.wantType {
			case "mqtt":
				if _, ok := client.(*mqttclient.Client); !ok {
					t.Fatalf("实际类型%T，期望*mqttclient.Client", client)
				}
			}
		})
	}
}
