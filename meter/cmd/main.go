package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"MOCK_COLLECT/common/config"
	"MOCK_COLLECT/meter/app"
)

// main 是Go程序的入口。
//
// 入口只负责调用run并处理最终错误，具体启动流程已经交给app包。
func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		// run返回前已经完成停止goroutine和关闭串口，
		// 因此这里使用Fatal退出不会跳过重要的清理工作。
		slog.Error("程序异常停止", "error", err)
		os.Exit(1)
	}
}

// run 读取配置、监听系统退出信号，并启动应用。
//
// 把这部分放在独立函数中，是为了确保defer stop()先执行，
// 然后错误才返回main统一处理。
func run() error {
	cfg, err := config.LoadAppConfig()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}

	// signal.NotifyContext把Ctrl+C和系统停止信号转换成ctx.Done()通知。
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	application := app.New(cfg)
	return application.Run(ctx)
}
