package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func minimalDictionary() string {
	return `{"exact_strings":{"Hello":"你好"}}`
}

func makeUpdateZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPrepareToolUpdateAndChecksum(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	files := map[string]string{
		"antigravity-v2.33.0/设置与维护.exe":                  "MZcli",
		"antigravity-v2.33.0/antigravity-cn.exe":         "MZgui",
		"antigravity-v2.33.0/translations_settings.json": minimalDictionary(),
	}
	zipData := makeUpdateZip(t, files)
	sum := sha256.Sum256(zipData)
	zipHash := hex.EncodeToString(sum[:])
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v2.33.0",
			"assets": []map[string]any{
				{"name": "antigravity-chinese-v2.33.0.zip", "browser_download_url": baseURL + "/release.zip", "size": len(zipData)},
				{"name": "checksums.txt", "browser_download_url": baseURL + "/checksums.txt"},
			},
		})
	})
	mux.HandleFunc("/release.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipData)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  antigravity-chinese-v2.33.0.zip\n", zipHash)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	baseURL = server.URL

	rel, err := fetchLatestRelease(context.Background(), server.URL+"/api", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v2.33.0" {
		t.Fatalf("tag=%s", rel.Tag)
	}
	staged, err := prepareToolUpdate(context.Background(), rel, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{staged.CLIExe, staged.GUIExe, staged.Dictionary} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("缺少 %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(staged.CLIExe); err != nil || string(data) != "MZcli" {
		t.Fatalf("CLI 解压内容异常: %v %q", err, data)
	}
}

func TestFetchLatestReleaseTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"tag_name":"v2.33.0","assets":[]}`))
	}))
	defer server.Close()
	client := &http.Client{Timeout: 30 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := fetchLatestRelease(ctx, server.URL, client); err == nil {
		t.Fatal("超时应返回错误")
	}
}

func TestPrepareToolUpdateChecksumFailure(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	zipData := makeUpdateZip(t, map[string]string{
		"设置与维护.exe":                  "MZcli",
		"antigravity-cn.exe":         "MZgui",
		"translations_settings.json": minimalDictionary(),
	})
	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/release.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipData) })
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  release.zip\n"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	baseURL = server.URL
	rel := &toolRelease{
		Tag: "v2.33.0", ZipName: "release.zip",
		ZipURL: baseURL + "/release.zip", ChecksumsURL: baseURL + "/checksums.txt",
	}
	if _, err := prepareToolUpdate(context.Background(), rel, server.Client(), nil); err == nil {
		t.Fatal("校验失败时不应继续")
	}
}

func TestExtractToolUpdateRejectsMissingAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.zip")
	writeZip(t, good, map[string]string{
		"../nested/设置与维护.exe":               "MZcli",
		"nested/antigravity-cn.exe":         "MZgui",
		"nested/translations_settings.json": minimalDictionary(),
	})
	staged, err := extractToolUpdate(good, filepath.Join(dir, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged.Dictionary); err != nil {
		t.Fatal("安全解压后缺少词典")
	}
	bad := filepath.Join(dir, "bad.zip")
	writeZip(t, bad, map[string]string{
		"设置与维护.exe":                  "not-pe",
		"antigravity-cn.exe":         "MZgui",
		"translations_settings.json": minimalDictionary(),
	})
	if _, err := extractToolUpdate(bad, filepath.Join(dir, "stage2")); err == nil {
		t.Fatal("非 PE 文件应被拒绝")
	}
	missing := filepath.Join(dir, "missing.zip")
	writeZip(t, missing, map[string]string{"antigravity-cn.exe": "MZgui"})
	if _, err := extractToolUpdate(missing, filepath.Join(dir, "stage3")); err == nil {
		t.Fatal("缺少文件应被拒绝")
	}
}

func TestUpdateTransactionRollbackAndCommit(t *testing.T) {
	target := t.TempDir()
	stagedDir := t.TempDir()
	for name, body := range map[string]string{
		"设置与维护.exe":                  "MZnewcli",
		"antigravity-cn.exe":         "MZnewgui",
		"translations_settings.json": minimalDictionary(),
	} {
		if err := os.WriteFile(filepath.Join(stagedDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"设置与维护.exe":                  "MZoldcli",
		"antigravity-cn.exe":         "MZoldgui",
		"translations_settings.json": `{"exact_strings":{"Old":"旧"}}`,
	} {
		if err := os.WriteFile(filepath.Join(target, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staged := stagedToolUpdate{
		Dir:        stagedDir,
		CLIExe:     filepath.Join(stagedDir, "设置与维护.exe"),
		GUIExe:     filepath.Join(stagedDir, "antigravity-cn.exe"),
		Dictionary: filepath.Join(stagedDir, "translations_settings.json"),
	}
	tx, err := beginUpdateTransaction(target, staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "antigravity-cn.exe")); string(data) != "MZnewgui" {
		t.Fatalf("更新未生效: %q", data)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "antigravity-cn.exe")); string(data) != "MZoldgui" {
		t.Fatalf("回滚失败: %q", data)
	}

	tx2, err := beginUpdateTransaction(target, staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Apply(); err != nil {
		t.Fatal(err)
	}
	tx2.Commit()
	if _, err := os.Stat(tx2.BackupDir); !os.IsNotExist(err) {
		t.Fatal("提交后回滚目录未清理")
	}
}

func TestUpdateModeErrorPersistsForTerminalNotice(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if code := updateModeError("模拟下载失败"); code != 1 {
		t.Fatalf("更新失败退出码 = %d, 期望 1", code)
	}
	if got := consumePendingUpdateError(); got != "模拟下载失败" {
		t.Fatalf("待显示更新错误 = %q", got)
	}
}

func TestRestartedLauncherUsesVisibleConsole(t *testing.T) {
	path := filepath.Join(`C:\test`, "antigravity-cn.exe")
	cmd := newLauncherCommand(path, "--updated", "v2.33.1")
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNewConsole == 0 {
		t.Fatal("更新后重启启动器必须创建新的可见控制台")
	}
	if cmd.Dir != `C:\test` {
		t.Fatalf("启动器工作目录 = %q", cmd.Dir)
	}
}
