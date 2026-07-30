// Package storage 设备元数据持久化
// 用 JSON 文件存储设备列表，简单可靠，无需数据库
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store 设备元数据存储
type Store struct {
	path string
	mu   sync.RWMutex
}

// New 创建存储，path 是 JSON 文件路径
func New(dataDir string) *Store {
	return &Store{
		path: filepath.Join(dataDir, "devices.json"),
	}
}

// LoadAll 加载所有设备
// 返回 map[id]Device 便于上层快速索引
func (s *Store) LoadAll() (map[string]map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]map[string]interface{}), nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return make(map[string]map[string]interface{}), nil
	}

	var m map[string]map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SaveAll 保存所有设备
func (s *Store) SaveAll(devices map[string]map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(devices, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
