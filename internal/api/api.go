// Package api HTTP API 与 WebSocket 接口
package api

import (
	"net/http"
	"time"

	"nvr/internal/stream"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Server API 服务
type Server struct {
	log     *zap.Logger
	streams *stream.Manager
	go2rtcAPIBase string // go2rtc 的 API 地址，用于代理 WebRTC 等请求
}

// New 创建 API 服务
func New(log *zap.Logger, streams *stream.Manager, go2rtcAPIBase string) *Server {
	return &Server{
		log:           log,
		streams:       streams,
		go2rtcAPIBase: go2rtcAPIBase,
	}
}

// Register 注册业务路由（设备管理等）
func (s *Server) Register(r *gin.Engine) {
	api := r.Group("/api/v1")
	{
		api.GET("/devices", s.listDevices)
		api.GET("/devices/:id", s.getDevice)
		api.POST("/devices", s.addDevice)
		api.DELETE("/devices/:id", s.deleteDevice)
	}
}

// RegisterProxy 注册到 go2rtc 的反向代理（WebRTC/流接口）
func (s *Server) RegisterProxy(r *gin.Engine) error {
	return s.registerProxy(r)
}

// ---------- 设备管理 ----------

func (s *Server) listDevices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"devices": s.streams.ListDevices()})
}

func (s *Server) getDevice(c *gin.Context) {
	id := c.Param("id")
	d, ok := s.streams.GetDevice(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备不存在"})
		return
	}
	c.JSON(http.StatusOK, d)
}

// addDeviceRequest 添加设备请求体
type addDeviceRequest struct {
	ID      string `json:"id" binding:"required"`           // 设备 ID（也是 go2rtc 流名）
	Name    string `json:"name" binding:"required"`         // 显示名称
	Type    string `json:"type" binding:"required"`         // rtsp / xiaomi
	Source  string `json:"source" binding:"required"`       // go2rtc 源地址
	Enabled *bool  `json:"enabled"`                         // 默认 true
}

func (s *Server) addDevice(c *gin.Context) {
	var req addDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	d := &stream.Device{
		ID:      req.ID,
		Name:    req.Name,
		Type:    stream.DeviceType(req.Type),
		Source:  req.Source,
		Enabled: enabled,
	}
	if err := s.streams.AddDevice(d); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.log.Info("添加设备", zap.String("id", d.ID), zap.String("name", d.Name))
	c.JSON(http.StatusCreated, d)
}

func (s *Server) deleteDevice(c *gin.Context) {
	id := c.Param("id")
	if err := s.streams.RemoveDevice(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	s.log.Info("删除设备", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// HealthCheck 健康检查接口（供 Docker / 负载均衡使用）
func (s *Server) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().Unix(),
	})
}
