package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/app"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if err := run(); err != nil {
		log.Fatal("统一网关异常停止：", err)
	}
}

func run() error {
	meterConfig, err := config.LoadAppConfig()
	if err != nil {
		return fmt.Errorf("读取电表与MQTT配置失败: %w", err)
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// 正式网关现在只接入电表，因此直接复用电表应用的完整生命周期：
	// 打开串口、等待MQTT就绪、启动采集和DO控制，并在退出时安全关闭资源。
	return app.New(meterConfig).Run(ctx)
}
