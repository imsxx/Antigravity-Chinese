package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaultsAndRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if cfg := loadConfig(); !cfg.UpdateCheckEnabled {
		t.Fatal("默认应开启更新检查")
	}
	cfg := launcherConfig{UpdateCheckEnabled: false}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig(); got.UpdateCheckEnabled {
		t.Fatal("配置未持久化")
	}
}

func TestStateMigrationAndRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	legacy := `{"patchedClientVersion":"2.32.1","dictionarySha256":"abc","bundleSha256":"def","unknownUiStrings":["A"]}`
	if err := os.MkdirAll(appDataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	st := loadState()
	if st.SchemaVersion != 1 || st.PatchedClientVersion != "2.32.1" || len(st.UnknownUIStrings) != 1 {
		t.Fatalf("状态迁移失败: %+v", st)
	}
	st.LastNewCandidateCount = 12
	if err := saveState(st); err != nil {
		t.Fatal(err)
	}
	got := loadState()
	if got.LastNewCandidateCount != 12 || got.UpdatedAt == "" {
		t.Fatalf("状态读写失败: %+v", got)
	}
	if err := os.WriteFile(statePath(), []byte(`{"schemaVersion":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadState(); got.SchemaVersion != 0 {
		t.Fatal("未知 schema 应被忽略")
	}
}

func TestLogRotation(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, err := ensureAppDataDir(); err != nil {
		t.Fatal(err)
	}
	large := make([]byte, (1<<20)+1)
	if err := os.WriteFile(logPath(), large, 0o644); err != nil {
		t.Fatal(err)
	}
	logEvent("test")
	if st, err := os.Stat(logPath()); err != nil || st.Size() == 0 {
		t.Fatal("新日志未创建")
	}
	if _, err := os.Stat(logPath() + ".1"); err != nil {
		t.Fatal("旧日志未轮转")
	}
	if filepath.Base(logPath()) != "launcher.log" {
		t.Fatal("日志路径异常")
	}
}

func TestPendingUpdateErrorRoundTrip(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if got := consumePendingUpdateError(); got != "" {
		t.Fatalf("初始不应有更新错误: %q", got)
	}
	if err := savePendingUpdateError("下载更新包失败"); err != nil {
		t.Fatal(err)
	}
	if got := consumePendingUpdateError(); got != "下载更新包失败" {
		t.Fatalf("读取更新错误 = %q", got)
	}
	if got := consumePendingUpdateError(); got != "" {
		t.Fatalf("更新错误应在读取后清除: %q", got)
	}
}
