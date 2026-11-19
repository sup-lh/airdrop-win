package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DeviceName        string `json:"deviceName"`
	Port              int    `json:"port"`
	SaveDir           string `json:"saveDir"`
	Token             string `json:"token"`
	ConfirmReceive    bool   `json:"confirmReceive"`
	AutoCopyText      bool   `json:"autoCopyText"`
	ClipboardAutoPush bool   `json:"clipboardAutoPush"`
	SelectedInterface string `json:"selectedInterface"`
	NotifyOnReceive   bool   `json:"notifyOnReceive"`
	OpenWindowOnStart bool   `json:"openWindowOnStart"`
}

type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func DefaultDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "AirDropWin")
}

func DefaultSaveDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads", "AirDropWin")
}

func GenerateToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewManager() (*Manager, error) {
	dir := DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "Windows-PC"
	}

	cfg := Config{
		DeviceName:        host,
		Port:              53318,
		SaveDir:           DefaultSaveDir(),
		Token:             GenerateToken(),
		ConfirmReceive:    false,
		AutoCopyText:      true,
		ClipboardAutoPush: false,
		SelectedInterface: "",
		NotifyOnReceive:   true,
		OpenWindowOnStart: true,
	}

	m := &Manager{
		path: path,
		cfg:  cfg,
	}

	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m.cfg)
	}

	// Ensure sane defaults for missing fields
	if m.cfg.DeviceName == "" {
		m.cfg.DeviceName = host
	}
	if m.cfg.Port <= 0 || m.cfg.Port > 65535 {
		m.cfg.Port = 53318
	}
	if m.cfg.SaveDir == "" {
		m.cfg.SaveDir = DefaultSaveDir()
	}
	if len(m.cfg.Token) < 16 {
		m.cfg.Token = GenerateToken()
	}

	_ = os.MkdirAll(m.cfg.SaveDir, 0o755)
	_ = m.saveLocked()

	return m, nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Update(fn func(*Config)) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.cfg)
	if m.cfg.SaveDir != "" {
		_ = os.MkdirAll(m.cfg.SaveDir, 0o755)
	}
	err := m.saveLocked()
	return m.cfg, err
}

func (m *Manager) ResetToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.Token = GenerateToken()
	err := m.saveLocked()
	return m.cfg.Token, err
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
