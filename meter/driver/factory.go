// Package driver 根据配置创建具体品牌的电表驱动。
package driver

import (
	"fmt"
	"strings"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/device"
	"MOCK_COLLECT/meter/driver/qs300"
	"MOCK_COLLECT/meter/modbus"
)

// New 在共享RS485总线上创建一块具体型号的电表。
// 将来新增品牌时，在这里增加一个分支并实现device.Device即可。
func New(cfg config.MeterConfig, bus *modbus.Bus) (device.Device, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "qs300":
		if bus == nil {
			return nil, fmt.Errorf("创建QS300驱动需要有效的RS485总线")
		}
		return qs300.New(bus.Client(cfg.SlaveID)), nil
	default:
		return nil, fmt.Errorf("不支持的电表驱动 %q", cfg.Driver)
	}
}
