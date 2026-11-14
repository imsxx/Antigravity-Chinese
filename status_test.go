package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
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
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBundle(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.zip")
	writeZip(t, good, map[string]string{"main.js": "console.log(1)", "index.html": "<html></html>"})
	if ok, hash := validateBundle(good); !ok || hash == "" {
		t.Fatal("有效资源包未被接受")
	}
	missingIndex := filepath.Join(dir, "missing.zip")
	writeZip(t, missingIndex, map[string]string{"main.js": "console.log(1)"})
	if ok, _ := validateBundle(missingIndex); ok {
		t.Fatal("缺少 index.html 的资源包不应被接受")
	}
	corrupt := filepath.Join(dir, "corrupt.zip")
	writeZip(t, corrupt, map[string]string{"main.js": "console.log(1)", "index.html": "<html></html>"})
	data, err := os.ReadFile(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(corrupt, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := validateBundle(corrupt); ok {
		t.Fatal("损坏的资源包不应被接受")
	}

	corruptOther := filepath.Join(dir, "corrupt-other.zip")
	writeZip(t, corruptOther, map[string]string{
		"main.js":    "console.log(1)",
		"index.html": "<html></html>",
		"extra.bin":  strings.Repeat("unrelated-entry ", 512),
	})
	zr, err := zip.OpenReader(corruptOther)
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	for _, f := range zr.File {
		if f.Name == "extra.bin" {
			offset, err = f.DataOffset()
			break
		}
	}
	zr.Close()
	if err != nil || offset <= 0 {
		t.Fatalf("无法定位测试条目: %v", err)
	}
	data, err = os.ReadFile(corruptOther)
	if err != nil {
		t.Fatal(err)
	}
	data[offset+2] ^= 0xff
	if err := os.WriteFile(corruptOther, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := validateBundle(corruptOther); ok {
		t.Fatal("非 main/index 条目损坏时资源包也不应被接受")
	}
}

func TestDesktopShortcutPath(t *testing.T) {
	path, err := desktopShortcutPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "Antigravity CN.lnk" {
		t.Fatalf("快捷方式路径异常: %s", path)
	}
}
