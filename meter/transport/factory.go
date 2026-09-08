package transport

import (
	"fmt"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/common/mqttclient"
)

// 编译期接口检查。
//
// 如果以后某个客户端漏掉了 Client 接口要求的方法，
// 编译器会在这里立即报错。
var _ Client = (*mqttclient.Client)(nil)

// NewClient 根据 TRANSPORT 配置创建通信客户端。
//
// 工厂（factory）是“根据条件负责创建对象”的代码。
// 把创建逻辑放在这里以后，app、collector 和 controller
// 都不需要直接依赖具体的MQTT客户端类型。
func NewClient(cfg config.AppConfig) (Client, error) {
	switch cfg.Transport {
	case config.TransportMQTT:
		return mqttclient.NewClient(cfg.MQTT, cfg.MeterIDs()), nil

	default:
		return nil, fmt.Errorf("不支持的通信方式 %q", cfg.Transport)
	}
}
