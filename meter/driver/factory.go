// Package driver 根据配置创建具体品牌的电表驱动。
package driver

import (
	"fmt"
	"strings"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/driver/qs300"
)

// Open 是应用层创建电表的唯一入口。
// 将来新增品牌时，在这里增加一个分支并实现device.Device即可。
func Open(cfg config.MeterConfig) (device.Device, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "qs300":
		return qs300.Open(cfg)
	default:
		return nil, fmt.Errorf("不支持的电表驱动 %q", cfg.Driver)
	}
}
