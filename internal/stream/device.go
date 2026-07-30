// Package stream 摄像头设备模型定义
package stream

import "time"

// DeviceType 摄像头类型
type DeviceType string

const (
	DeviceTypeRTSP   DeviceType = "rtsp"   // 标准 RTSP 摄像头
	DeviceTypeXiaomi DeviceType = "xiaomi" // 小米摄像头
)

// Device 摄像头设备
type Device struct {
	ID        string     `json:"id"`         // 设备唯一 ID
	Name      string     `json:"name"`       // 显示名称
	Type      DeviceType `json:"type"`       // 设备类型
	Source    string     `json:"source"`     // go2rtc 源地址（RTSP URL 或 xiaomi:// URL）
	Enabled   bool       `json:"enabled"`    // 是否启用
	CreatedAt time.Time  `json:"created_at"` // 创建时间
}
