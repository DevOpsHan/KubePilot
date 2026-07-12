package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"aiops/internal/app"
	"aiops/internal/config"
	"aiops/internal/logging"
)

func main() {
	logger, err := logging.New()
	if err != nil {
		panic(err)
	}
	defer func() { _ = logger.Sync() }()

	// 配置文件路径,默认 config.yaml,可用环境变量 AIOPS_CONFIG 覆盖。
	configPath := os.Getenv("AIOPS_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatal("配置加载失败", zap.Error(err))
	}

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Fatal("应用初始化失败", zap.Error(err))
	}

	// 监听系统信号,实现优雅关闭。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.Sync.AutoSyncEnabled() {
		application.Server.StartAutoSync(
			ctx,
			time.Duration(cfg.Sync.IntervalSeconds)*time.Second,
			time.Duration(cfg.Sync.InitialDelaySeconds)*time.Second,
		)
	} else {
		logger.Info("集群自动同步调度器已关闭")
	}
	if cfg.Inspection.InspectionEnabled() {
		application.Server.StartInspectionScheduler(
			ctx,
			time.Duration(cfg.Inspection.IntervalSeconds)*time.Second,
			time.Duration(cfg.Inspection.InitialDelaySeconds)*time.Second,
		)
	} else {
		logger.Info("定时巡检调度器已关闭")
	}

	go func() {
		logger.Info("AIOps Agent 启动", zap.String("addr", cfg.Server.HTTPAddr))
		if err := application.Server.Echo().Start(cfg.Server.HTTPAddr); err != nil {
			logger.Info("HTTP 服务已停止", zap.Error(err))
		}
	}()

	<-ctx.Done()
	logger.Info("收到关闭信号,正在优雅关闭…")
	if err := application.Server.Echo().Shutdown(context.Background()); err != nil {
		logger.Error("优雅关闭失败", zap.Error(err))
	}
}
