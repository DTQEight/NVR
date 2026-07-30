// Package api go2rtc 接口反向代理
package api

import (
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerProxy 注册到 go2rtc 的反向代理
// 把 go2rtc 的 WebUI、API、WebRTC 信令全部代理到 NVR 单端口下，
// 这样部署时只需暴露 NVR 的 8080 端口，无需额外暴露 1984。
//
// 代理映射：
//   /api/webrtc, /api/stream, /api/streams  → go2rtc API（WebRTC 播放、流管理）
//   /static/*                                → go2rtc WebUI 静态资源
//   /go2rtc/*                                → go2rtc 完整 WebUI（含小米登录页）
func (s *Server) registerProxy(r *gin.Engine) error {
	target, err := url.Parse(s.go2rtcAPIBase)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// 1. go2rtc API 接口（NVR 自身的 /api/v1 不冲突）
	go2rtcPaths := []string{"/api/webrtc", "/api/stream", "/api/streams"}
	for _, p := range go2rtcPaths {
		r.Any(p, func(c *gin.Context) {
			s.handleProxy(c, proxy)
		})
		r.Any(p+"/*any", func(c *gin.Context) {
			s.handleProxy(c, proxy)
		})
	}

	// 2. go2rtc WebUI 静态资源（go2rtc 的 HTML 用绝对路径 /static/xxx 引用）
	//    NVR 自己的静态资源在 /web 下，不冲突
	r.Any("/static/*any", func(c *gin.Context) {
		s.handleProxy(c, proxy)
	})

	// 3. go2rtc 完整 WebUI（含小米账号登录页）
	//    访问 /go2rtc/ 即可打开 go2rtc 的 WebUI
	//    需要去掉 /go2rtc 前缀，让 go2rtc 收到根路径请求
	r.Any("/go2rtc/*any", func(c *gin.Context) {
		c.Request.URL.Path = strings.TrimPrefix(c.Request.URL.Path, "/go2rtc")
		if c.Request.URL.Path == "" {
			c.Request.URL.Path = "/"
		}
		s.handleProxy(c, proxy)
	})
	// 兼容不带尾斜杠的 /go2rtc
	r.GET("/go2rtc", func(c *gin.Context) {
		c.Request.URL.Path = "/"
		s.handleProxy(c, proxy)
	})

	return nil
}

// handleProxy 执行反向代理
func (s *Server) handleProxy(c *gin.Context, proxy *httputil.ReverseProxy) {
	// 保留原始查询参数（如 ?src=cam1）
	proxy.ServeHTTP(c.Writer, c.Request)
}
