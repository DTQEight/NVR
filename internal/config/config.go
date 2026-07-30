// Package config 配置加载与管理
package config

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

// Config 全局配置结构
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Go2RTC  Go2RTCConfig  `mapstructure:"go2rtc"`
	Storage StorageConfig `mapstructure:"storage"`
	Record  RecordConfig  `mapstructure:"record"`
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// Go2RTCConfig go2rtc 配置
// 支持两种模式：
//   - external=true :  连接已部署的 go2rtc（推荐 NAS 场景），NVR 通过 HTTP API 管理流
//   - external=false : NVR 自己拉起 go2rtc 子进程（单机部署场景）
type Go2RTCConfig struct {
	External   bool   `mapstructure:"external"`    // 是否使用外部已部署的 go2rtc
	APIBase    string `mapstructure:"api_base"`    // 外部模式：go2rtc HTTP API 地址，如 http://192.168.1.10:1984
	RTSPBase   string `mapstructure:"rtsp_base"`   // 外部模式：go2rtc RTSP 地址，如 rtsp://192.168.1.10:8554
	BinaryPath string `mapstructure:"binary_path"` // 进程模式：go2rtc 二进制路径
	ConfigPath string `mapstructure:"config_path"` // 进程模式：go2rtc.yaml 路径
	APIPort    int    `mapstructure:"api_port"`    // 进程模式：go2rtc HTTP API 端口
	RTSPPort   int    `mapstructure:"rtsp_port"`   // 进程模式：go2rtc RTSP 端口
}

// StorageConfig 存储配置
type StorageConfig struct {
	DataDir    string `mapstructure:"data_dir"`    // 数据目录（设备元数据等）
	RecordDir  string `mapstructure:"record_dir"`  // 录像目录
}

// RecordConfig 录像策略配置
type RecordConfig struct {
	SegmentDuration int    `mapstructure:"segment_duration"` // 单段录像时长（分钟）
	KeepDays        int    `mapstructure:"keep_days"`        // 录像保留天数
	Format          string `mapstructure:"format"`           // 封装格式 mp4/mkv
}

var (
	current *Config
	mu      sync.RWMutex
)

// Load 从文件加载配置
// 支持 NVR_ 前缀的环境变量覆盖，如 NVR_GO2RTC_API_BASE 覆盖 go2rtc.api_base
// 文件不存在时返回 os.IsNotExist(err) 可识别的错误，便于上层回退
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	// 启用环境变量覆盖（Docker 部署友好）
	v.SetEnvPrefix("NVR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	applyDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		// 把 viper 的 ConfigFileNotFoundError 转成 os.IsNotExist 可识别
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, os.ErrNotExist
		}
		// 也可能是 fs.PathError
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}

	return finalize(v)
}

// LoadDefaults 不读文件，仅用默认值 + 环境变量构造配置
// 适用于 Docker 等纯环境变量驱动的场景
func LoadDefaults() (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("NVR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	applyDefaults(v)
	return finalize(v)
}

// applyDefaults 设置所有默认值
func applyDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)

	// go2rtc 默认：外部模式，连接本机 go2rtc
	v.SetDefault("go2rtc.external", true)
	v.SetDefault("go2rtc.api_base", "http://127.0.0.1:1984")
	v.SetDefault("go2rtc.rtsp_base", "rtsp://127.0.0.1:8554")
	v.SetDefault("go2rtc.binary_path", "go2rtc")
	v.SetDefault("go2rtc.config_path", "config/go2rtc.yaml")
	v.SetDefault("go2rtc.api_port", 1984)
	v.SetDefault("go2rtc.rtsp_port", 8554)

	v.SetDefault("storage.data_dir", "data")
	v.SetDefault("storage.record_dir", "recordings")

	v.SetDefault("record.segment_duration", 30)
	v.SetDefault("record.keep_days", 7)
	v.SetDefault("record.format", "mp4")
}

// finalize 解析并缓存配置
func finalize(v *viper.Viper) (*Config, error) {
	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	mu.Lock()
	current = &c
	mu.Unlock()
	return &c, nil
}

// Get 获取当前配置
func Get() *Config {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
