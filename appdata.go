package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type launcherConfig struct {
	UpdateCheckEnabled bool `json:"updateCheckEnabled"`
}

type launcherState struct {
	SchemaVersion         int      `json:"schemaVersion"`
	PatchedClientVersion  string   `json:"patchedClientVersion"`
	DictionarySHA256      string   `json:"dictionarySha256"`
	BundleSHA256          string   `json:"bundleSha256"`
	UnknownUIStrings      []string `json:"unknownUiStrings"`
	LastNewCandidateCount int      `json:"lastNewCandidateCount"`
	UpdatedAt             string   `json:"updatedAt"`
}

var logMu sync.Mutex

func appDataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if d, err := os.UserConfigDir(); err == nil {
			base = d
		}
	}
	return filepath.Join(base, "Antigravity-Chinese")
}

func ensureAppDataDir() (string, error) {
	dir := appDataDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func configPath() string {
	return filepath.Join(appDataDir(), "config.json")
}

func statePath() string {
	return filepath.Join(appDataDir(), "state.json")
}

func logPath() string {
	return filepath.Join(appDataDir(), "logs", "launcher.log")
}

func updateErrorPath() string {
	return filepath.Join(appDataDir(), "update-error.txt")
}

func defaultConfig() launcherConfig {
	return launcherConfig{UpdateCheckEnabled: true}
}

func loadConfig() launcherConfig {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig()
	}
	return cfg
}

func saveConfig(cfg launcherConfig) error {
	if _, err := ensureAppDataDir(); err != nil {
		return err
	}
	return writeJSONAtomic(configPath(), cfg)
}

func loadState() launcherState {
	var st launcherState
	data, err := os.ReadFile(statePath())
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return launcherState{}
	}
	// 早期状态文件没有 schemaVersion;字段兼容时直接迁移到当前结构。
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.SchemaVersion != 1 {
		return launcherState{}
	}
	return st
}

func saveState(st launcherState) error {
	if _, err := ensureAppDataDir(); err != nil {
		return err
	}
	st.SchemaVersion = 1
	st.UpdatedAt = time.Now().Format(time.RFC3339)
	return writeJSONAtomic(statePath(), st)
}

func removeState() {
	_ = os.Remove(statePath())
}

func savePendingUpdateError(message string) error {
	if _, err := ensureAppDataDir(); err != nil {
		return err
	}
	return os.WriteFile(updateErrorPath(), []byte(strings.TrimSpace(message)+"\n"), 0o644)
}

func consumePendingUpdateError() string {
	data, err := os.ReadFile(updateErrorPath())
	if err != nil {
		return ""
	}
	_ = os.Remove(updateErrorPath())
	return strings.TrimSpace(string(data))
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := replaceFileWithTemp(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func logEvent(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()

	if _, err := ensureAppDataDir(); err != nil {
		return
	}
	p := logPath()
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		_ = os.Remove(p + ".1")
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}
