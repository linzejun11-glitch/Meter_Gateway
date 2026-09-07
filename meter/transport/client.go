// Package transport 定义上层业务代码使用的统一通信接口。
//
// transport 的中文含义是“传输层”。采集和控制模块只依赖这里的 Client，
// 不需要直接处理Python MQTT Broker的Topic、分片或重传细节。
package transport

import (
	"context"

	"MOCK_COLLECT/common/protocol"
)

// Client 是所有通信客户端必须实现的统一接口。
type Client interface {
	// Run 启动网络连接，并保持运行直到 ctx 被取消。
	//
	// 返回 nil 表示正常停止；返回非 nil 表示发生了无法继续的错误。
	Run(ctx context.Context) error

	// Ready 返回首次连接就绪通知。
	//
	// MQTT完成登录和必要Topic订阅后才算就绪。
	Ready() <-chan struct{}

	// Publish 发送一条上行消息。
	Publish(message any) error

	// Commands 返回服务器下发的控制命令通道。
	Commands() <-chan protocol.SwitchControl
}
