// NVR 主程序入口
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nvr/internal/api"
	"nvr/internal/config"
	"nvr/internal/storage"
	"nvr/internal/stream"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	flag.Parse()

	// 初始化日志
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Printf("初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// 加载配置（文件不存在时回退到默认值 + 环境变量）
	cfg, err := config.Load(*configPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("配置文件不存在，使用默认值 + 环境变量",
				zap.String("path", *configPath),
			)
			cfg, err = config.LoadDefaults()
			if err != nil {
				logger.Fatal("加载默认配置失败", zap.Error(err))
			}
		} else {
			logger.Fatal("加载配置失败", zap.Error(err))
		}
	}
	logger.Info("配置加载完成",
		zap.String("host", cfg.Server.Host),
		zap.Int("port", cfg.Server.Port),
	)

	// 初始化存储层（设备元数据）
	store := storage.New(cfg.Storage.DataDir)

	// 启动 go2rtc（外部模式或进程模式）
	streamMgr := stream.New(logger, cfg, store)
	if err := streamMgr.Start(); err != nil {
		logger.Fatal("启动 go2rtc 失败", zap.Error(err))
	}
	defer streamMgr.Stop()
	if cfg.Go2RTC.External {
		logger.Info("go2rtc 外部模式", zap.String("api_base", cfg.Go2RTC.APIBase))
	} else {
		logger.Info("go2rtc 进程模式已启动",
			zap.Int("api_port", cfg.Go2RTC.APIPort),
			zap.Int("rtsp_port", cfg.Go2RTC.RTSPPort),
		)
	}

	// 启动 HTTP 服务
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	// go2rtc API 反向代理目标地址
	var go2rtcAPIBase string
	if cfg.Go2RTC.External {
		go2rtcAPIBase = cfg.Go2RTC.APIBase
	} else {
		go2rtcAPIBase = fmt.Sprintf("http://127.0.0.1:%d", cfg.Go2RTC.APIPort)
	}
	apiSrv := api.New(logger, streamMgr, go2rtcAPIBase)
	apiSrv.Register(r)
	if err := apiSrv.RegisterProxy(r); err != nil {
		logger.Fatal("注册 go2rtc 代理失败", zap.Error(err))
	}

	// 静态文件服务（前端 Web UI）
	r.Static("/web", "./web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/web")
	})

	httpSrv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: r,
	}

	// 优雅关闭
	go func() {
		logger.Info("HTTP 服务启动", zap.String("addr", httpSrv.Addr))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("HTTP 服务异常", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在关闭服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Error("HTTP 服务关闭超时", zap.Error(err))
	}
	logger.Info("服务已退出")
}
