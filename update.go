package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxUpdateZipSize  = 256 << 20
	maxChecksumSize   = 1 << 20
	maxUpdateFileSize = 64 << 20
)

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type toolRelease struct {
	Tag          string
	ZipName      string
	ZipURL       string
	ChecksumsURL string
}

type stagedToolUpdate struct {
	WorkDir    string
	Dir        string
	CLIExe     string
	GUIExe     string
	Dictionary string
	Version    string
}

type updateProgress func(stage string, percent int)

func fetchLatestRelease(ctx context.Context, apiURL string, client *http.Client) (*toolRelease, error) {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	if apiURL == "" {
		apiURL = repoAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Antigravity-Chinese-Launcher")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新服务返回 HTTP %d", resp.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析更新信息失败: %w", err)
	}
	if release.Draft || release.Prerelease {
		return nil, fmt.Errorf("最新发布不是稳定版")
	}
	if _, err := parseToolVersion(release.Tag); err != nil {
		return nil, fmt.Errorf("发布版本号无效: %w", err)
	}

	out := &toolRelease{Tag: release.Tag}
	for _, asset := range release.Assets {
		name := strings.ToLower(filepath.Base(asset.Name))
		switch {
		case name == "checksums.txt":
			if out.ChecksumsURL != "" {
				return nil, fmt.Errorf("Release 中存在多个 checksums.txt")
			}
			out.ChecksumsURL = asset.URL
		case strings.HasPrefix(name, "antigravity-chinese-") && strings.HasSuffix(name, ".zip"):
			if out.ZipURL != "" {
				return nil, fmt.Errorf("Release 中存在多个更新压缩包")
			}
			out.ZipName = filepath.Base(asset.Name)
			out.ZipURL = asset.URL
		}
	}
	return out, nil
}

type toolSemver struct {
	Major int
	Minor int
	Patch int
	Pre   []string
}

func parseToolVersion(v string) (toolSemver, error) {
	var out toolSemver
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" {
		return out, fmt.Errorf("空版本号")
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	mainPart := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		mainPart = s[:i]
		out.Pre = strings.Split(strings.ToLower(s[i+1:]), ".")
	}
	parts := strings.Split(mainPart, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, fmt.Errorf("版本号格式无效: %s", v)
	}
	nums := []*int{&out.Major, &out.Minor, &out.Patch}
	for i, p := range parts {
		if p == "" {
			return out, fmt.Errorf("版本号格式无效: %s", v)
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("版本号格式无效: %s", v)
		}
		*nums[i] = n
	}
	return out, nil
}

func compareToolVersions(a, b string) (int, error) {
	va, err := parseToolVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseToolVersion(b)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{va.Major, vb.Major}, {va.Minor, vb.Minor}, {va.Patch, vb.Patch}} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
	}
	if len(va.Pre) == 0 && len(vb.Pre) == 0 {
		return 0, nil
	}
	if len(va.Pre) == 0 {
		return 1, nil
	}
	if len(vb.Pre) == 0 {
		return -1, nil
	}
	for i := 0; i < len(va.Pre) && i < len(vb.Pre); i++ {
		if va.Pre[i] == vb.Pre[i] {
			continue
		}
		na, ea := strconv.Atoi(va.Pre[i])
		nb, eb := strconv.Atoi(vb.Pre[i])
		switch {
		case ea == nil && eb == nil:
			if na < nb {
				return -1, nil
			}
			return 1, nil
		case ea == nil:
			return -1, nil
		case eb == nil:
			return 1, nil
		default:
			if va.Pre[i] < vb.Pre[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	if len(va.Pre) < len(vb.Pre) {
		return -1, nil
	}
	if len(va.Pre) > len(vb.Pre) {
		return 1, nil
	}
	return 0, nil
}

func isNewerToolVersion(current, latest string) bool {
	cmp, err := compareToolVersions(current, latest)
	return err == nil && cmp < 0
}

func parseChecksums(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	scanner.Buffer(make([]byte, 64<<10), maxChecksumSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var hash, name string
		if strings.HasPrefix(line, "SHA256 (") {
			closePos := strings.Index(line, ") = ")
			if closePos < 0 {
				return nil, fmt.Errorf("无法解析校验行: %s", line)
			}
			name = line[len("SHA256 ("):closePos]
			hash = strings.TrimSpace(line[closePos+4:])
		} else {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return nil, fmt.Errorf("无法解析校验行: %s", line)
			}
			hash = fields[0]
			name = strings.TrimPrefix(fields[1], "*")
		}
		hash = strings.ToLower(hash)
		if len(hash) != 64 {
			return nil, fmt.Errorf("校验值长度无效: %s", name)
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return nil, fmt.Errorf("校验值格式无效: %s", name)
		}
		name = strings.ToLower(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
		if name == "." || name == "" {
			return nil, fmt.Errorf("校验文件名无效")
		}
		if old, ok := out[name]; ok && old != hash {
			return nil, fmt.Errorf("校验文件中存在冲突: %s", name)
		}
		out[name] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("校验文件为空")
	}
	return out, nil
}

func checksumForAsset(sums map[string]string, name string) (string, error) {
	want := strings.ToLower(filepath.Base(name))
	for candidate, hash := range sums {
		if strings.EqualFold(filepath.Base(candidate), want) {
			return hash, nil
		}
	}
	return "", fmt.Errorf("校验文件中缺少 %s", name)
}

func verifyFileSHA256(path, want string) error {
	got, err := sha256File(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("SHA256 校验失败: 期望 %s,实际 %s", want, got)
	}
	return nil
}

func prepareToolUpdate(ctx context.Context, rel *toolRelease, client *http.Client, progress updateProgress) (stagedToolUpdate, error) {
	var staged stagedToolUpdate
	if rel == nil || rel.ZipURL == "" || rel.ChecksumsURL == "" {
		return staged, fmt.Errorf("更新信息不完整")
	}
	if _, err := parseToolVersion(rel.Tag); err != nil {
		return staged, err
	}
	base, err := ensureAppDataDir()
	if err != nil {
		return staged, err
	}
	updateRoot := filepath.Join(base, "update")
	tagDir := safeVersionDir(rel.Tag)
	if entries, err := os.ReadDir(updateRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != tagDir {
				_ = os.RemoveAll(filepath.Join(updateRoot, entry.Name()))
			}
		}
	}
	workDir := filepath.Join(updateRoot, tagDir)
	stageDir := filepath.Join(workDir, "stage")
	if err := os.RemoveAll(workDir); err != nil {
		return staged, err
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return staged, err
	}
	staged.WorkDir = workDir
	staged.Dir = stageDir
	staged.Version = rel.Tag
	report := func(stage string, pct int) {
		if progress != nil {
			progress(stage, pct)
		}
	}

	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	report("正在读取更新校验信息…", 2)
	sumData, err := downloadBytes(ctx, client, rel.ChecksumsURL, maxChecksumSize)
	if err != nil {
		return staged, fmt.Errorf("下载校验文件失败: %w", err)
	}
	sums, err := parseChecksums(sumData)
	if err != nil {
		return staged, err
	}
	wantZipHash, err := checksumForAsset(sums, rel.ZipName)
	if err != nil {
		return staged, err
	}

	zipPath := filepath.Join(workDir, "release.zip")
	report("正在下载新版本…", 5)
	if err := downloadToFile(ctx, client, rel.ZipURL, zipPath, maxUpdateZipSize, func(done, total int64) {
		pct := 5
		if total > 0 {
			pct = 5 + int(float64(done)/float64(total)*70)
			if pct > 75 {
				pct = 75
			}
		}
		report(fmt.Sprintf("正在下载新版本… %.1f / %.1f MB", float64(done)/(1<<20), float64(total)/(1<<20)), pct)
	}); err != nil {
		return staged, fmt.Errorf("下载更新包失败: %w", err)
	}
	report("正在校验更新包…", 78)
	if err := verifyFileSHA256(zipPath, wantZipHash); err != nil {
		return staged, err
	}
	report("正在解压更新包…", 84)
	extracted, err := extractToolUpdate(zipPath, stageDir)
	if err != nil {
		return staged, err
	}
	extracted.WorkDir = workDir
	extracted.Version = rel.Tag
	report("更新包已准备就绪", 96)
	return extracted, nil
}

func safeVersionDir(tag string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, tag)
	if clean == "" || clean == "." || clean == ".." {
		return "release"
	}
	return clean
}

func downloadBytes(ctx context.Context, client *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Antigravity-Chinese-Launcher")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("响应超过大小限制")
	}
	return data, nil
}

func downloadToFile(ctx context.Context, client *http.Client, url, dst string, max int64, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Antigravity-Chinese-Launcher")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	_ = os.Remove(tmp)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		_ = os.Remove(tmp)
	}()

	total := resp.ContentLength
	done := int64(0)
	buf := make([]byte, 128<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if done+int64(n) > max {
				return fmt.Errorf("下载内容超过大小限制")
			}
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if total > 0 && done != total {
		return fmt.Errorf("下载不完整: %d/%d 字节", done, total)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return nil
}

func extractToolUpdate(zipPath, destDir string) (stagedToolUpdate, error) {
	var staged stagedToolUpdate
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return staged, fmt.Errorf("打开更新包失败: %w", err)
	}
	defer zr.Close()

	wanted := map[string]string{
		maintenanceExeName: maintenanceExeName,
		launcherExeName:    launcherExeName,
		dictionaryFileName: dictionaryFileName,
	}
	found := make(map[string]bool)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return staged, err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(f.Name, "\\", "/")))
		outName, ok := wanted[base]
		if !ok {
			continue
		}
		if found[base] {
			return staged, fmt.Errorf("更新包中存在重复文件: %s", outName)
		}
		if f.UncompressedSize64 > maxUpdateFileSize {
			return staged, fmt.Errorf("更新包文件过大: %s", outName)
		}
		found[base] = true
		outPath := filepath.Join(destDir, outName)
		rc, err := f.Open()
		if err != nil {
			return staged, err
		}
		copyErr := extractZipFile(rc, outPath)
		rc.Close()
		if copyErr != nil {
			return staged, fmt.Errorf("解压更新文件失败 %s: %w", outName, copyErr)
		}
	}
	staged.CLIExe = filepath.Join(destDir, maintenanceExeName)
	staged.GUIExe = filepath.Join(destDir, launcherExeName)
	staged.Dictionary = filepath.Join(destDir, dictionaryFileName)
	if err := validPEFile(staged.CLIExe); err != nil {
		return staged, fmt.Errorf("更新包中的控制台程序无效: %w", err)
	}
	if err := validPEFile(staged.GUIExe); err != nil {
		return staged, fmt.Errorf("更新包中的启动器无效: %w", err)
	}
	if _, err := loadRules(staged.Dictionary); err != nil {
		return staged, fmt.Errorf("更新包中的词典无效: %w", err)
	}
	return staged, nil
}

func extractZipFile(rc io.Reader, outPath string) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, maxUpdateFileSize+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxUpdateFileSize {
		return fmt.Errorf("文件超过大小限制")
	}
	return nil
}

func validPEFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var head [2]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return err
	}
	if head[0] != 'M' || head[1] != 'Z' {
		return fmt.Errorf("不是有效的 Windows 可执行文件")
	}
	return nil
}

func spawnUpdateHelper(exeDir string, staged stagedToolUpdate, version string) error {
	base, err := ensureAppDataDir()
	if err != nil {
		return err
	}
	current, err := os.Executable()
	if err != nil {
		return err
	}
	helperDir := filepath.Join(base, "update")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return err
	}
	helper := filepath.Join(helperDir, "updater.exe")
	if err := copyFile(current, helper); err != nil {
		return fmt.Errorf("复制更新助手失败: %w", err)
	}
	cmd := exec.Command(helper,
		"--apply-update",
		"--pid", strconv.Itoa(os.Getpid()),
		"--source", staged.Dir,
		"--target", exeDir,
		"--version", version,
	)
	cmd.Dir = exeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	logEvent("已启动更新助手: pid=%d target=%s version=%s", cmd.Process.Pid, exeDir, version)
	_ = cmd.Process.Release()
	return nil
}

type updateFileEntry struct {
	Name    string
	Staged  string
	Target  string
	Backup  string
	Existed bool
}

type updateTransaction struct {
	Target    string
	BackupDir string
	Entries   []updateFileEntry
}

func beginUpdateTransaction(target string, staged stagedToolUpdate) (*updateTransaction, error) {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(absTarget); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("安装目录不存在")
	}
	if err := validPEFile(staged.CLIExe); err != nil {
		return nil, err
	}
	if err := validPEFile(staged.GUIExe); err != nil {
		return nil, err
	}
	if _, err := loadRules(staged.Dictionary); err != nil {
		return nil, err
	}

	backupDir := filepath.Join(absTarget, ".antigravity-cn-update-"+strconv.Itoa(os.Getpid()))
	if err := os.RemoveAll(backupDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建更新回滚目录失败: %w", err)
	}
	tx := &updateTransaction{Target: absTarget, BackupDir: backupDir}
	files := []struct {
		name   string
		staged string
	}{
		{dictionaryFileName, staged.Dictionary},
		{maintenanceExeName, staged.CLIExe},
		{launcherExeName, staged.GUIExe},
	}
	for _, f := range files {
		targetPath := filepath.Join(absTarget, f.name)
		backupPath := filepath.Join(backupDir, f.name)
		existed := fileExists(targetPath)
		if existed {
			if err := copyFile(targetPath, backupPath); err != nil {
				_ = os.RemoveAll(backupDir)
				return nil, fmt.Errorf("备份 %s 失败: %w", f.name, err)
			}
		}
		tx.Entries = append(tx.Entries, updateFileEntry{
			Name: f.name, Staged: f.staged, Target: targetPath, Backup: backupPath, Existed: existed,
		})
	}
	// 兼容旧用户:词典单独保留一份 .bak,便于手动回退自定义词典。
	dictTarget := filepath.Join(absTarget, dictionaryFileName)
	if fileExists(dictTarget) {
		if err := copyFile(dictTarget, dictTarget+".bak"); err != nil {
			_ = os.RemoveAll(backupDir)
			return nil, fmt.Errorf("备份旧词典失败: %w", err)
		}
	}
	return tx, nil
}

func (tx *updateTransaction) Apply() error {
	rollback := func(cause error) error {
		if err := tx.Rollback(); err != nil {
			return fmt.Errorf("%w; 回滚失败: %v", cause, err)
		}
		return cause
	}
	for _, entry := range tx.Entries {
		tmp := entry.Target + ".update.tmp"
		_ = os.Remove(tmp)
		if err := copyFile(entry.Staged, tmp); err != nil {
			return rollback(fmt.Errorf("准备 %s 失败: %w", entry.Name, err))
		}
		if err := replaceFileWithTemp(tmp, entry.Target); err != nil {
			_ = os.Remove(tmp)
			return rollback(fmt.Errorf("替换 %s 失败: %w", entry.Name, err))
		}
	}
	if err := validPEFile(filepath.Join(tx.Target, maintenanceExeName)); err != nil {
		return rollback(err)
	}
	if err := validPEFile(filepath.Join(tx.Target, launcherExeName)); err != nil {
		return rollback(err)
	}
	if _, err := loadRules(filepath.Join(tx.Target, dictionaryFileName)); err != nil {
		return rollback(err)
	}
	return nil
}

func (tx *updateTransaction) Rollback() error {
	var errs []string
	for _, entry := range tx.Entries {
		if entry.Existed {
			if err := restoreFromBackup(entry.Backup, entry.Target); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", entry.Name, err))
			}
		} else if err := os.Remove(entry.Target); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("%s: %v", entry.Name, err))
		}
	}
	if err := os.RemoveAll(tx.BackupDir); err != nil {
		errs = append(errs, fmt.Sprintf("清理回滚目录: %v", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (tx *updateTransaction) Commit() {
	_ = os.RemoveAll(tx.BackupDir)
}

const (
	processSynchronize = 0x00100000
	waitObject0        = 0
	waitTimeout        = 0x102
	createNewConsole   = 0x00000010
)

var (
	updateKernel32          = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess         = updateKernel32.NewProc("OpenProcess")
	procWaitForSingleObject = updateKernel32.NewProc("WaitForSingleObject")
	procCloseHandle         = updateKernel32.NewProc("CloseHandle")
)

func applyUpdateMode(args []string) int {
	var pid int
	var source, target, version string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pid", "--source", "--target", "--version":
			if i+1 >= len(args) {
				return updateModeError("更新参数不完整")
			}
			switch args[i] {
			case "--pid":
				pid, _ = strconv.Atoi(args[i+1])
			case "--source":
				source = args[i+1]
			case "--target":
				target = args[i+1]
			case "--version":
				version = args[i+1]
			}
			i++
		default:
			return updateModeError("未知更新参数: " + args[i])
		}
	}
	if pid <= 0 || source == "" || target == "" || version == "" {
		return updateModeError("更新参数不完整")
	}
	if _, err := parseToolVersion(version); err != nil {
		return updateModeError("更新版本号无效")
	}
	if err := waitForProcessExit(pid, 120*time.Second); err != nil {
		return updateModeError(fmt.Sprintf("等待旧启动器退出失败: %v", err))
	}
	staged := stagedToolUpdate{
		Dir:        source,
		CLIExe:     filepath.Join(source, maintenanceExeName),
		GUIExe:     filepath.Join(source, launcherExeName),
		Dictionary: filepath.Join(source, dictionaryFileName),
		Version:    version,
	}
	tx, err := beginUpdateTransaction(target, staged)
	if err != nil {
		return updateModeError(fmt.Sprintf("准备更新失败: %v", err))
	}
	if err := tx.Apply(); err != nil {
		return updateModeError(fmt.Sprintf("更新失败，已保留旧版本: %v", err))
	}
	gui := filepath.Join(target, launcherExeName)
	cmd, err := startLauncherInNewConsole(gui, "--updated", version)
	if err != nil {
		_ = tx.Rollback()
		_, _ = startLauncherInNewConsole(gui)
		return updateModeError(fmt.Sprintf("新版本启动失败，已回滚: %v", err))
	}
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
	}()
	select {
	case err := <-exited:
		if err != nil {
			rollbackErr := tx.Rollback()
			_, _ = startLauncherInNewConsole(gui)
			if rollbackErr != nil {
				return updateModeError(fmt.Sprintf("新版本启动失败且回滚不完整: %v; %v", err, rollbackErr))
			}
			return updateModeError(fmt.Sprintf("新版本启动失败，已回滚: %v", err))
		}
	case <-time.After(800 * time.Millisecond):
		// 新终端启动器已进入正常流程或开始打开客户端，提交本次更新。
	}
	tx.Commit()
	cleanupStagedUpdate(source)
	logEvent("工具已更新到 %s", version)
	return 0
}

func startLauncherInNewConsole(path string, args ...string) (*exec.Cmd, error) {
	cmd := newLauncherCommand(path, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func newLauncherCommand(path string, args ...string) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	return cmd
}

func cleanupStagedUpdate(stageDir string) {
	base, err := filepath.Abs(filepath.Join(appDataDir(), "update"))
	if err != nil {
		return
	}
	stage, err := filepath.Abs(stageDir)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(base, stage)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	_ = os.RemoveAll(filepath.Dir(stage))
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	handle, _, err := procOpenProcess.Call(processSynchronize, 0, uintptr(pid))
	if handle == 0 {
		// 进程可能已经先退出。
		if errno, ok := err.(syscall.Errno); ok && uint32(errno) == 87 {
			return nil
		}
		return err
	}
	defer procCloseHandle.Call(handle)
	r, _, waitErr := procWaitForSingleObject.Call(handle, uintptr(timeout/time.Millisecond))
	switch uint32(r) {
	case waitObject0:
		return nil
	case waitTimeout:
		return fmt.Errorf("等待超时")
	default:
		return waitErr
	}
}

func updateModeError(msg string) int {
	logEvent("自更新失败: %s", msg)
	_ = savePendingUpdateError(msg)
	return 1
}
