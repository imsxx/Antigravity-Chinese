package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type patchStatus struct {
	AppDir           string `json:"appDir"`
	ClientVersion    string `json:"clientVersion"`
	Patched          bool   `json:"patched"`
	BundleValid      bool   `json:"bundleValid"`
	DictionarySHA256 string `json:"dictionarySha256"`
	BundleSHA256     string `json:"bundleSha256"`
	StateMatches     bool   `json:"stateMatches"`
	NeedsPatch       bool   `json:"needsPatch"`
	Error            string `json:"error,omitempty"`
}

func inspectPatchStatus(appDir, exeDir string) patchStatus {
	st := patchStatus{AppDir: appDir}
	if appDir == "" {
		st.NeedsPatch = true
		st.Error = "未检测到 Antigravity 安装目录"
		return st
	}

	st.ClientVersion = getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
	if st.ClientVersion == "" {
		st.Error = "无法读取 Antigravity.exe 版本"
	}
	dictPath := filepath.Join(exeDir, "translations_settings.json")
	if h, err := sha256File(dictPath); err == nil {
		st.DictionarySHA256 = h
	} else if st.Error == "" {
		st.Error = fmt.Sprintf("读取词典失败: %v", err)
	}

	resources := filepath.Join(appDir, "resources")
	asarPath := filepath.Join(resources, "app.asar")
	if _, files, err := unpackAsar(asarPath); err != nil {
		if st.Error == "" {
			st.Error = fmt.Sprintf("解析 app.asar 失败: %v", err)
		}
	} else if ls, ok := files[lsScriptPath]; ok {
		st.Patched = strings.Contains(toValidUTF8(ls), patchMarker)
	} else if st.Error == "" {
		st.Error = "app.asar 中未找到 dist/languageServer.js"
	}

	bundlePath := filepath.Join(resources, "web_bundle.zip")
	if valid, hash := validateBundle(bundlePath); valid {
		st.BundleValid = true
		st.BundleSHA256 = hash
	}

	state := loadState()
	st.StateMatches = state.SchemaVersion == 1 &&
		state.PatchedClientVersion == st.ClientVersion &&
		state.DictionarySHA256 == st.DictionarySHA256 &&
		state.BundleSHA256 == st.BundleSHA256
	st.NeedsPatch = !st.Patched || !st.BundleValid || !st.StateMatches
	return st
}

func validateBundle(path string) (bool, string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false, ""
	}
	defer zr.Close()
	foundMain, foundIndex := false, false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return false, ""
		}
		_, err = io.Copy(io.Discard, rc)
		rc.Close()
		if err != nil {
			return false, ""
		}
		if f.Name == "main.js" && f.UncompressedSize64 > 0 {
			foundMain = true
		}
		if f.Name == "index.html" && f.UncompressedSize64 > 0 {
			foundIndex = true
		}
	}
	if !foundMain || !foundIndex {
		return false, ""
	}
	hash, err := sha256File(path)
	if err != nil {
		return false, ""
	}
	return true, hash
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
