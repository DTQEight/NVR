package stream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nvr/internal/config"
	"nvr/internal/storage"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

func TestWriteConfigQuotesListenAddresses(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Go2RTC: config.Go2RTCConfig{
			ConfigPath: filepath.Join(dir, "go2rtc.yaml"),
			APIPort:    1984,
			RTSPPort:   8554,
		},
	}
	legacyConfig := []byte("api:\n  listen: :1984\nxiaomi:\n  token: preserved\n")
	if err := os.WriteFile(cfg.Go2RTC.ConfigPath, legacyConfig, 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	m := New(zap.NewNop(), cfg, storage.New(filepath.Join(dir, "data")))
	m.devices["front-door"] = &Device{ID: "front-door", Source: "rtsp://camera/stream", Enabled: true}

	if err := m.writeConfig(); err != nil {
		t.Fatalf("writeConfig() error = %v", err)
	}

	data, err := os.ReadFile(cfg.Go2RTC.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `listen: ":1984"`) || !strings.Contains(content, `listen: ":8554"`) {
		t.Fatalf("listen addresses must be quoted, got:\n%s", content)
	}
	if !strings.Contains(content, "xiaomi:\n  token: preserved") {
		t.Fatalf("existing go2rtc settings must be preserved, got:\n%s", content)
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated config must be valid YAML: %v", err)
	}
}
