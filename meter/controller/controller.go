// Package controller 负责接收控制命令、操作DO并回复执行结果。
//
// controller 的中文含义是“控制器”。它不负责定时采集，
// 只处理服务器主动下发的开关控制。
package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"MOCK_COLLECT/common/protocol"
	"MOCK_COLLECT/meter/modbus"
	"MOCK_COLLECT/meter/transport"

	"github.com/goburrow/serial"
)

const maxCachedACKs = 50

// Controller 保存控制循环运行时需要的依赖。
type Controller struct {
	meter   *modbus.Client
	client  transport.Client
	slaveID byte
	cache   *ackCache
}

// New 创建控制器，但不会立即开始监听命令。
func New(
	meter *modbus.Client,
	client transport.Client,
	slaveID byte,
) *Controller {
	return &Controller{
		meter:   meter,
		client:  client,
		slaveID: slaveID,
		cache:   newACKCache(maxCachedACKs),
	}
}

// Run 持续等待服务器下发控制命令。
func (c *Controller) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case command, ok := <-c.client.Commands():
			// 当前客户端不会主动关闭Commands通道，
			// 保留ok检查可以防止未来关闭通道后不断读到零值。
			if !ok {
				return
			}

			if cached, exists := c.cache.get(command.RequestID); exists {
				log.Printf(
					"收到重复控制命令%s，直接返回历史ACK",
					command.RequestID,
				)
				c.publishACK(cached)
				continue
			}

			ack := c.execute(command)
			c.cache.put(command.RequestID, ack)
			c.publishACK(ack)

			// 控制完成后立即重新读取一次DO状态，
			// 并向当前服务器补发最新的DI/DO状态。
			switches, err := c.meter.ReadSwitches()
			if err != nil {
				log.Printf("控制后读取开关状态失败：%v", err)
				continue
			}

			status := protocol.NewSwitchStatus(c.slaveID, switches)
			if err := c.client.Publish(status); err != nil {
				log.Printf("控制后上报开关状态失败：%v", err)
			}
		}
	}
}

// execute 校验并执行一条DO控制命令。
func (c *Controller) execute(
	command protocol.SwitchControl,
) protocol.ControlACK {
	// 先创建默认失败的ACK。
	// 只有全部校验和Modbus控制成功后，Success才会设置为true。
	ack := protocol.ControlACK{
		MessageType: protocol.MessageTypeControlACK,
		RequestID:   command.RequestID,
		MeterID:     command.MeterID,
		Channel:     command.Channel,
		Success:     false,
		Timestamp:   protocol.FormatTimestamp(time.Now()),
	}

	if command.RequestID == "" {
		ack.ErrorCode = "INVALID_COMMAND"
		ack.Message = "request_id is required"
		return ack
	}

	if command.MeterID != int(c.slaveID) {
		ack.ErrorCode = "INVALID_METER_ID"
		ack.Message = fmt.Sprintf("configured meter_id is %d", c.slaveID)
		return ack
	}

	if command.Channel != 1 && command.Channel != 2 {
		ack.ErrorCode = "INVALID_CHANNEL"
		ack.Message = "channel must be 1 or 2"
		return ack
	}

	// DesiredState是*bool（布尔指针）。
	//
	// nil表示JSON完全没有desired_state字段，
	// 非nil且值为false表示服务器明确要求断开，两种情况不能混淆。
	if command.DesiredState == nil {
		ack.ErrorCode = "INVALID_COMMAND"
		ack.Message = "desired_state is required"
		return ack
	}

	ack.DesiredState = *command.DesiredState

	// channel=1 -> 业务DO1 -> 仪表物理DO0 -> 端子15/16；
	// channel=2 -> 业务DO2 -> 仪表物理DO1 -> 端子17/18。
	actual, known, err := c.meter.ControlDigitalOutput(
		command.Channel,
		*command.DesiredState,
	)

	if known {
		actualCopy := actual
		ack.ActualState = &actualCopy
	}

	if err != nil {
		ack.ErrorCode = controlErrorCode(err)
		ack.Message = err.Error()

		log.Printf(
			"控制失败：request_id=%s meter=%d channel=%d error=%v",
			command.RequestID,
			command.MeterID,
			command.Channel,
			err,
		)
		return ack
	}

	ack.Success = true
	ack.ErrorCode = ""
	ack.Message = "control succeeded"

	log.Printf(
		"控制成功：request_id=%s meter=%d channel=%d state=%t",
		command.RequestID,
		command.MeterID,
		command.Channel,
		*command.DesiredState,
	)

	return ack
}

// publishACK 将控制执行结果交给当前通信客户端。
//
// 不同通信客户端会把它转换为各自服务器要求的回复格式。
func (c *Controller) publishACK(ack protocol.ControlACK) {
	if err := c.client.Publish(ack); err != nil {
		log.Printf("控制ACK发送失败：%v", err)
	}
}

// controlErrorCode 将底层错误转换成服务器容易判断的业务错误码。
func controlErrorCode(err error) string {
	if errors.Is(err, serial.ErrTimeout) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "MODBUS_TIMEOUT"
	}

	var exception *modbus.ExceptionError
	if errors.As(err, &exception) {
		return "MODBUS_EXCEPTION"
	}

	if errors.Is(err, modbus.ErrStateMismatch) {
		return "STATE_MISMATCH"
	}

	if strings.Contains(strings.ToLower(err.Error()), "crc") {
		return "MODBUS_CRC_ERROR"
	}

	return "MODBUS_ERROR"
}
