// Package api go2rtc 接口反向代理
package api

import (
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerProxy 注册到 go2rtc 的反向代理
// 把 /api/webrtc、/api/stream 等接口透传给 go2rtc，
// 这样前端只需访问 NVR 的 8080 端口即可使用 WebRTC 播放。
func (s *Server) registerProxy(r *gin.Engine) error {
	target, err := url.Parse(s.go2rtcAPIBase)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// 透传给 go2rtc 的接口前缀（NVR 自身的 /api/v1 不冲突）
	go2rtcPaths := []string{"/api/webrtc", "/api/stream", "/api/streams"}
	for _, p := range go2rtcPaths {
		r.Any(p, func(c *gin.Context) {
			s.handleProxy(c, proxy)
		})
		r.Any(p+"/*any", func(c *gin.Context) {
			s.handleProxy(c, proxy)
		})
	}

	// go2rtc 自带的 stream.html 播放页（调试用，正式版用自己的 UI）
	r.GET("/go2rtc/*any", func(c *gin.Context) {
		// 去掉 /go2rtc 前缀，让 go2rtc 收到的是根路径请求
		c.Request.URL.Path = strings.TrimPrefix(c.Request.URL.Path, "/go2rtc")
		if c.Request.URL.Path == "" {
			c.Request.URL.Path = "/"
		}
		s.handleProxy(c, proxy)
	})

	return nil
}

// handleProxy 执行反向代理
func (s *Server) handleProxy(c *gin.Context, proxy *httputil.ReverseProxy) {
	// 保留原始查询参数（如 ?src=cam1）
	proxy.ServeHTTP(c.Writer, c.Request)
}
