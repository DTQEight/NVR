// Package stream 设备管理 + go2rtc 流管理
// 支持两种模式：
//   - 外部模式（external=true）: 连接已部署的 go2rtc，通过 HTTP API 增删流，元数据本地持久化
//   - 进程模式（external=false）: NVR 自己拉起 go2rtc 子进程，写 yaml 配置
package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"nvr/internal/config"
	"nvr/internal/storage"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Manager 管理设备元数据与 go2rtc 流
type Manager struct {
	log     *zap.Logger
	cfg     *config.Config
	store   *storage.Store

	mu      sync.RWMutex
	devices map[string]*Device

	// 进程模式专用
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	done       chan struct{}
	restarting bool
}

// quotedYAMLString forces values that YAML could interpret as syntax (for
// example ":1984") to be emitted as quoted strings.
type quotedYAMLString string

var legacyListenAddress = regexp.MustCompile(`(?m)^(\s*listen:\s*):(\d+)(\s*(?:#.*)?)$`)

func (s quotedYAMLString) MarshalYAML() (interface{}, error) {
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: string(s),
		Style: yaml.DoubleQuotedStyle,
	}, nil
}

// repairLegacyListenAddresses migrates configs written by older versions,
// which emitted a leading colon without YAML quotes.
func repairLegacyListenAddresses(data []byte) []byte {
	return legacyListenAddress.ReplaceAll(data, []byte(`${1}":${2}"${3}`))
}

// New 创建管理器
func New(log *zap.Logger, cfg *config.Config, store *storage.Store) *Manager {
	return &Manager{
		log:     log,
		cfg:     cfg,
		store:   store,
		devices: make(map[string]*Device),
	}
}

// Start 启动管理器
// - 外部模式：加载本地元数据，把 enabled 设备同步到 go2rtc
// - 进程模式：启动 go2rtc 子进程
func (m *Manager) Start() error {
	if err := m.loadFromStore(); err != nil {
		return fmt.Errorf("加载设备元数据失败: %w", err)
	}

	if m.cfg.Go2RTC.External {
		m.log.Info("使用外部 go2rtc 模式",
			zap.String("api_base", m.cfg.Go2RTC.APIBase),
		)
		// 同步设备到 go2rtc
		return m.syncAllToGo2RTC()
	}

	// 进程模式：启动子进程
	return m.startProcess()
}

// Stop 停止管理器（进程模式下停止 go2rtc）
func (m *Manager) Stop() error {
	if m.cfg.Go2RTC.External {
		return nil
	}
	return m.stopProcess()
}

// ---------- 设备 CRUD ----------

// AddDevice 添加设备
func (m *Manager) AddDevice(d *Device) error {
	m.mu.Lock()
	if _, exists := m.devices[d.ID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("设备 ID 已存在: %s", d.ID)
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	m.devices[d.ID] = d
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	// 同步到 go2rtc
	if d.Enabled {
		return m.addStreamToGo2RTC(d.ID, d.Source)
	}
	return nil
}

// RemoveDevice 删除设备
func (m *Manager) RemoveDevice(id string) error {
	m.mu.Lock()
	d, exists := m.devices[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("设备不存在: %s", id)
	}
	delete(m.devices, id)
	m.mu.Unlock()

	if err := m.persist(); err != nil {
		return err
	}
	if d.Enabled {
		_ = m.removeStreamFromGo2RTC(id) // 容错：go2rtc 没有也不算错
	}
	return nil
}

// ListDevices 列出所有设备
func (m *Manager) ListDevices() []*Device {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*Device, 0, len(m.devices))
	for _, d := range m.devices {
		list = append(list, d)
	}
	return list
}

// GetDevice 获取单个设备
func (m *Manager) GetDevice(id string) (*Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	return d, ok
}

// ---------- 持久化 ----------

// loadFromStore 从本地存储加载设备元数据
func (m *Manager) loadFromStore() error {
	all, err := m.store.LoadAll()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, raw := range all {
		d := &Device{ID: id}
		// 简单字段提取
		if v, ok := raw["name"].(string); ok {
			d.Name = v
		}
		if v, ok := raw["type"].(string); ok {
			d.Type = DeviceType(v)
		}
		if v, ok := raw["source"].(string); ok {
			d.Source = v
		}
		if v, ok := raw["enabled"].(bool); ok {
			d.Enabled = v
		}
		if v, ok := raw["created_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				d.CreatedAt = t
			}
		}
		m.devices[id] = d
	}
	return nil
}

// persist 持久化设备列表
func (m *Manager) persist() error {
	m.mu.RLock()
	all := make(map[string]map[string]interface{}, len(m.devices))
	for id, d := range m.devices {
		all[id] = map[string]interface{}{
			"name":       d.Name,
			"type":       string(d.Type),
			"source":     d.Source,
			"enabled":    d.Enabled,
			"created_at": d.CreatedAt.Format(time.RFC3339),
		}
	}
	m.mu.RUnlock()
	return m.store.SaveAll(all)
}

// ---------- go2rtc HTTP API ----------

// go2rtcAPIClient 封装 go2rtc 的 streams API
// 文档：https://github.com/AlexxIT/go2rtc#api
//   GET    /api/streams          -> {name: src}
//   PUT    /api/streams?src=...  -> 创建/更新流（body 空，name 在 query）
//   DELETE /api/streams?name=... -> 删除流
//
// 实际 go2rtc 的 PUT 接口：
//   PUT /api/streams?src={url}&name={name}
// 或新版：
//   PUT /api/streams/{name}?src={url}

func (m *Manager) apiBase() string {
	if m.cfg.Go2RTC.External {
		return m.cfg.Go2RTC.APIBase
	}
	return fmt.Sprintf("http://127.0.0.1:%d", m.cfg.Go2RTC.APIPort)
}

// addStreamToGo2RTC 在 go2rtc 中创建/更新流
func (m *Manager) addStreamToGo2RTC(name, src string) error {
	u := fmt.Sprintf("%s/api/streams?src=%s&name=%s",
		m.apiBase(),
		url.QueryEscape(src),
		url.QueryEscape(name),
	)
	req, _ := http.NewRequest(http.MethodPut, u, nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("go2rtc 创建流失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("go2rtc 返回 %d: %s", resp.StatusCode, string(body))
	}
	m.log.Info("已同步流到 go2rtc", zap.String("name", name))
	return nil
}

// removeStreamFromGo2RTC 从 go2rtc 删除流
func (m *Manager) removeStreamFromGo2RTC(name string) error {
	u := fmt.Sprintf("%s/api/streams?name=%s",
		m.apiBase(),
		url.QueryEscape(name),
	)
	req, _ := http.NewRequest(http.MethodDelete, u, nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// syncAllToGo2RTC 启动时把所有 enabled 设备同步到 go2rtc
func (m *Manager) syncAllToGo2RTC() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.devices {
		if d.Enabled {
			if err := m.addStreamToGo2RTC(d.ID, d.Source); err != nil {
				m.log.Warn("同步流失败",
					zap.String("id", d.ID),
					zap.Error(err),
				)
			}
		}
	}
	return nil
}

// ListGo2RTCStreams 列出 go2rtc 当前所有流（用于调试/对账）
func (m *Manager) ListGo2RTCStreams() (map[string]string, error) {
	u := fmt.Sprintf("%s/api/streams", m.apiBase())
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ---------- 进程模式实现 ----------

// startProcess 启动 go2rtc 子进程
func (m *Manager) startProcess() error {
	if err := m.writeConfig(); err != nil {
		return fmt.Errorf("写入 go2rtc 配置失败: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.done = make(chan struct{})

	cmd := exec.CommandContext(ctx, m.cfg.Go2RTC.BinaryPath, "-config", m.cfg.Go2RTC.ConfigPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	m.log.Info("启动 go2rtc 进程",
		zap.String("binary", m.cfg.Go2RTC.BinaryPath),
		zap.String("config", m.cfg.Go2RTC.ConfigPath),
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 go2rtc 失败: %w", err)
	}
	m.cmd = cmd

	go m.watch()

	// 等待 go2rtc HTTP API 就绪（最多 10 秒）
	if err := m.waitForReady(10 * time.Second); err != nil {
		m.log.Warn("go2rtc 启动后 API 未就绪（可能仍正常，继续运行）", zap.Error(err))
	}
	return nil
}

// waitForReady 轮询 go2rtc HTTP API 直到就绪或超时
func (m *Manager) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	u := fmt.Sprintf("%s/api/streams", m.apiBase())
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("等待 go2rtc 就绪超时")
}

// watch 监控 go2rtc 进程
func (m *Manager) watch() {
	if m.cmd == nil {
		close(m.done)
		return
	}
	err := m.cmd.Wait()
	m.log.Warn("go2rtc 进程退出", zap.Error(err))
	close(m.done)

	m.mu.Lock()
	restarting := m.restarting
	m.mu.Unlock()

	if err != nil && !restarting {
		m.log.Info("5 秒后自动重启 go2rtc...")
		time.Sleep(5 * time.Second)
		if err := m.startProcess(); err != nil {
			m.log.Error("go2rtc 重启失败", zap.Error(err))
		}
	}
}

// stopProcess 停止 go2rtc 子进程
func (m *Manager) stopProcess() error {
	m.mu.Lock()
	m.restarting = true
	m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
	}
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
	}
	m.cmd = nil

	m.mu.Lock()
	m.restarting = false
	m.mu.Unlock()
	return nil
}

// writeConfig 进程模式：生成 go2rtc.yaml
// 采用"合并"模式：保留现有 yaml 中的 xiaomi/log 等用户手动配置的段，
// 只更新 api/rtsp/streams 段。这样用户在 go2rtc WebUI 登录小米账号后
// 保存的 xiaomi 段不会被覆盖。
func (m *Manager) writeConfig() error {
	m.mu.RLock()
	streams := make(map[string]string, len(m.devices))
	for _, d := range m.devices {
		if d.Enabled {
			streams[d.ID] = d.Source
		}
	}
	m.mu.RUnlock()

	// 读取现有配置（如有），保留未知字段
	existing := make(map[string]interface{})
	if data, err := os.ReadFile(m.cfg.Go2RTC.ConfigPath); err == nil {
		data = repairLegacyListenAddresses(data)
		if err := yaml.Unmarshal(data, existing); err != nil {
			return fmt.Errorf("解析 go2rtc 配置失败: %w", err)
		}
	}

	// 更新 NVR 管理的段
	existing["api"] = map[string]interface{}{
		"listen": quotedYAMLString(fmt.Sprintf(":%d", m.cfg.Go2RTC.APIPort)),
	}
	existing["rtsp"] = map[string]interface{}{
		"listen": quotedYAMLString(fmt.Sprintf(":%d", m.cfg.Go2RTC.RTSPPort)),
	}
	existing["streams"] = streams

	if dir := filepath.Dir(m.cfg.Go2RTC.ConfigPath); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(existing); err != nil {
		return err
	}
	enc.Close()
	return os.WriteFile(m.cfg.Go2RTC.ConfigPath, buf.Bytes(), 0644)
}

// RTSPURL 拼接标准 RTSP 源地址
func RTSPURL(host string, port int, user, password, path string) string {
	if user != "" {
		return fmt.Sprintf("rtsp://%s:%s@%s:%d/%s", user, password, host, port, path)
	}
	return fmt.Sprintf("rtsp://%s:%d/%s", host, port, path)
}
