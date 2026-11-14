// Antigravity 设置界面(Ctrl+,)一键中文化工具 —— Go 版引擎(仅标准库)。
//
// 与 patch_settings.py(Python 参考实现)保持输出逐字节一致:
// 同一份版本、同一份词典,打出的补丁内容必须完全相同(以 --verify 的 sha256 校验)。
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	bakMarkerSuffix = ".version"
	lsScriptPath    = "dist/languageServer.js"

	// 注入锚点与注入文本(逆向还原依赖此文本的逐字匹配,请勿修改)
	insertAfter = "'--enable_sidecars',\n        ];"
	injectText  = "\n        const _customBundle = path_1.default.join(process.resourcesPath, 'web_bundle.zip');" +
		"\n        if (fs.existsSync(_customBundle)) {" +
		"\n            args.push(`--web_bundle_path=${_customBundle}`);" +
		"\n        }"
	patchMarker = "_customBundle"
)

// ---------------------------------------------------------------- 路径 / 版本 / 进程

func getAntigravityDir(custom string) string {
	if custom != "" {
		if st, err := os.Stat(custom); err == nil && st.IsDir() {
			return custom
		}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	p := filepath.Join(local, "Programs", "antigravity")
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return ""
}

var (
	modVersion = syscall.NewLazyDLL("version.dll")
	procSize   = modVersion.NewProc("GetFileVersionInfoSizeW")
	procInfo   = modVersion.NewProc("GetFileVersionInfoW")
	procQuery  = modVersion.NewProc("VerQueryValueW")
)

// getFileVersion 通过 Win32 version.dll 读取文件版本号(主.次.构建),失败返回空串。
func getFileVersion(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	size, _, _ := procSize.Call(uintptr(unsafe.Pointer(p)), 0)
	if size == 0 {
		return ""
	}
	buf := make([]byte, size)
	ok, _, _ := procInfo.Call(uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&buf[0])))
	if ok == 0 {
		return ""
	}
	root, _ := syscall.UTF16PtrFromString("\\")
	var qp unsafe.Pointer
	var qlen uint32
	ok2, _, _ := procQuery.Call(uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(root)),
		uintptr(unsafe.Pointer(&qp)),
		uintptr(unsafe.Pointer(&qlen)))
	if ok2 == 0 || qp == nil || qlen < 16 {
		return ""
	}
	ffi := (*[4]uint32)(qp)
	ms := ffi[2]
	ls := ffi[3]
	return fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xFFFF, ls>>16)
}

func isAntigravityRunning() bool {
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq Antigravity.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Antigravity.exe")
}

// ---------------------------------------------------------------- 资源提取

// extractWebBundle 在 language_server.exe 中定位内嵌的界面资源 zip。
func extractWebBundle(lsExePath string) ([]byte, error) {
	fmt.Println("正在从 language_server.exe 读取内置界面资源...")
	data, err := os.ReadFile(lsExePath)
	if err != nil {
		return nil, err
	}
	sig := []byte{'P', 'K', 0x05, 0x06}
	local := []byte{'P', 'K', 0x03, 0x04}
	idx := 0
	for {
		rel := bytes.Index(data[idx:], sig)
		if rel < 0 {
			break
		}
		i := idx + rel
		if i+20 <= len(data) {
			cdSize := int64(binary.LittleEndian.Uint32(data[i+12:]))
			cdOff := int64(binary.LittleEndian.Uint32(data[i+16:]))
			zipStart := int64(i) - cdSize - cdOff
			if zipStart >= 0 && zipStart+4 <= int64(len(data)) &&
				bytes.Equal(data[zipStart:zipStart+4], local) {
				end := i + 22
				if end > len(data) {
					end = len(data)
				}
				candidate := data[zipStart:end]
				if zr, err := zip.NewReader(bytes.NewReader(candidate), int64(len(candidate))); err == nil {
					hasMain, hasIndex := false, false
					for _, f := range zr.File {
						if f.Name == "main.js" {
							hasMain = true
						}
						if f.Name == "index.html" {
							hasIndex = true
						}
					}
					if hasMain && hasIndex {
						return candidate, nil
					}
				}
			}
		}
		idx = i + 4
	}
	return nil, fmt.Errorf("未能在 language_server.exe 中定位到内置界面资源包!")
}

func readMainJS(appDir string) (string, error) {
	resources := filepath.Join(appDir, "resources")
	bundle, err := extractWebBundle(filepath.Join(resources, "bin", "language_server.exe"))
	if err != nil {
		return "", err
	}
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name == "main.js" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			return toValidUTF8(data), nil
		}
	}
	return "", fmt.Errorf("资源包中未找到 main.js")
}

// ---------------------------------------------------------------- 验证

func doVerify(appDir string, rules []*Rule) error {
	fmt.Println("干跑验证(不会写入客户端)...")
	mainJS, err := readMainJS(appDir)
	if err != nil {
		return err
	}
	fmt.Printf("main.js 大小: %d 字符\n", utf8.RuneCountInString(mainJS))
	patched, hit := applyRules(mainJS, rules)
	sum := sha256.Sum256([]byte(patched))
	fmt.Printf("补丁内容 sha256: %s\n", hex.EncodeToString(sum[:]))
	ok, total, _ := reportStats(rules, hit)
	if total > 0 && ok == total {
		fmt.Println("验证通过:全部规则命中。")
	}
	return nil
}

// ---------------------------------------------------------------- 应用与还原

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// toValidUTF8 等价于 Python 的 decode('utf-8', errors='ignore'):丢弃非法字节。
func toValidUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), "")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func refreshBackup(appAsar, appAsarBak, bakMarker, curVer string) error {
	if err := copyFile(appAsar, appAsarBak); err != nil {
		return err
	}
	return os.WriteFile(bakMarker, []byte(curVer), 0o644)
}

func orUnknown(s string) string {
	if s == "" {
		return "未知"
	}
	return s
}

type applyProgress func(stage string)

type applyResult struct {
	ClientVersion           string
	RulesHit                int
	RulesTotal              int
	Replacements            int
	NewCandidateCount       int
	CandidateCountAvailable bool
	Error                   string
}

func applyPatch(appDir string, rules []*Rule) bool {
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	_, ok := applyPatchWithProgress(appDir, exeDir, rules, nil)
	return ok
}

func applyPatchWithProgress(appDir, exeDir string, rules []*Rule, progress applyProgress) (result applyResult, ok bool) {
	resources := filepath.Join(appDir, "resources")
	appAsar := filepath.Join(resources, "app.asar")
	appAsarBak := filepath.Join(resources, "app.asar.bak")
	bakMarker := appAsarBak + bakMarkerSuffix
	lsExe := filepath.Join(resources, "bin", "language_server.exe")
	webBundleZip := filepath.Join(resources, "web_bundle.zip")
	tmpBundlePath := webBundleZip + fmt.Sprintf(".cn-tmp-%d", os.Getpid())
	tmpAsarPath := appAsar + fmt.Sprintf(".cn-tmp-%d", os.Getpid())
	defer os.Remove(tmpBundlePath)
	defer os.Remove(tmpAsarPath)
	stage := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	fail := func(msg string) (applyResult, bool) {
		result.Error = msg
		fmt.Printf("错误:%s\n", msg)
		logEvent("应用补丁失败: %s", msg)
		return result, false
	}

	if !fileExists(appAsar) || !fileExists(lsExe) {
		return fail(fmt.Sprintf("未能找到 Antigravity 必要核心文件: %s / %s", appAsar, lsExe))
	}

	curVer := getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
	if curVer == "" {
		curVer = "未知"
	}
	result.ClientVersion = curVer
	fmt.Printf("客户端版本: %s\n", curVer)
	state := loadState()
	versionChanged := state.PatchedClientVersion != "" && state.PatchedClientVersion != curVer
	shouldScanCandidates := state.PatchedClientVersion == "" || versionChanged

	// 打包自检:官方 asar 原样"解包 -> 重打包"必须与官方文件逐字节一致。
	// 若不一致说明 asar 结构假设有误(如数据区对齐),立即中止,绝不修改客户端。
	stage("校验客户端结构")
	fmt.Println("正在进行 asar 打包自检(解包 -> 重打包 必须与官方逐字节一致)...")
	headerCheck, filesCheck, err := unpackAsar(appAsar)
	if err != nil {
		fmt.Println("已中止,客户端未被修改。请运行 --scan 检查结构变化或反馈此问题。")
		return fail(fmt.Sprintf("asar 解析失败: %v", err))
	}
	selftestPath := appAsar + fmt.Sprintf(".selftest-%d.tmp", os.Getpid())
	defer os.Remove(selftestPath)
	if err := packAsar(headerCheck, filesCheck, selftestPath); err != nil {
		return fail(fmt.Sprintf("asar 重打包失败: %v", err))
	}
	roundtrip, err1 := os.ReadFile(selftestPath)
	original, err2 := os.ReadFile(appAsar)
	if err1 != nil || err2 != nil || !bytes.Equal(roundtrip, original) {
		fmt.Println("错误:asar 打包自检未通过(重打包结果与官方不一致)!")
		fmt.Println("已中止,客户端未被修改。请运行 --scan 检查结构变化或反馈此问题。")
		return fail("asar 打包自检未通过")
	}
	fmt.Println("打包自检通过。")

	header, filesData, err := unpackAsar(appAsar)
	if err != nil {
		return fail(fmt.Sprintf("asar 解析失败: %v", err))
	}
	lsRaw, ok := filesData[lsScriptPath]
	if !ok {
		return fail(fmt.Sprintf("app.asar 中未找到 %s", lsScriptPath))
	}
	lsCode := toValidUTF8(lsRaw)
	alreadyPatched := strings.Contains(lsCode, patchMarker)

	// 1. 备份官方原版(带版本标记,避免过期备份覆盖新版本)
	if !fileExists(appAsarBak) {
		fmt.Println("创建官方原版备份: app.asar.bak ...")
		if err := refreshBackup(appAsar, appAsarBak, bakMarker, curVer); err != nil {
			return fail(fmt.Sprintf("创建备份失败: %v", err))
		}
	} else {
		bakVer := ""
		if b, err := os.ReadFile(bakMarker); err == nil {
			bakVer = strings.TrimSpace(string(b))
		}
		if bakVer != curVer {
			if !alreadyPatched {
				fmt.Printf("检测到客户端已更新(备份版本 %s -> 当前 %s),正在刷新官方备份 ...\n", orUnknown(bakVer), curVer)
				if err := refreshBackup(appAsar, appAsarBak, bakMarker, curVer); err != nil {
					return fail(fmt.Sprintf("刷新备份失败: %v", err))
				}
			} else {
				fmt.Printf("提示:现有备份来自其他版本(%s),可能已过期;还原将优先采用逆向移除,不使用该备份。\n", orUnknown(bakVer))
			}
		} else {
			fmt.Println("已存在官方原版备份: app.asar.bak")
		}
	}

	// 2. 生成中文界面资源包
	stage("读取界面资源")
	bundleBytes, err := extractWebBundle(lsExe)
	if err != nil {
		return fail(err.Error())
	}
	inZf, err := zip.NewReader(bytes.NewReader(bundleBytes), int64(len(bundleBytes)))
	if err != nil {
		return fail(fmt.Sprintf("资源包解析失败: %v", err))
	}

	stage("读取界面资源")
	fmt.Println("正在汉化 main.js 设置界面文案...")
	var mainJS string
	for _, f := range inZf.File {
		if f.Name == "main.js" {
			rc, err := f.Open()
			if err != nil {
				return fail(fmt.Sprintf("读取 main.js 失败: %v", err))
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fail(fmt.Sprintf("读取 main.js 失败: %v", err))
			}
			mainJS = toValidUTF8(data)
		}
	}
	if mainJS == "" {
		return fail("资源包中未找到 main.js")
	}

	// 注入锚点必须在生成任何新资源之前确认。
	if !alreadyPatched && !strings.Contains(lsCode, insertAfter) {
		fmt.Println("建议运行: antigravity-chinese.exe --scan 检查。")
		return fail("未在 app.asar 中找到注入锚点,客户端结构可能已变化")
	}

	// 候选基线只在首次汉化或客户端版本变化时扫描;常规启动不承担这份开销。
	unknown := append([]string(nil), state.UnknownUIStrings...)
	if shouldScanCandidates {
		stage("分析新增文案")
		candidateStart := time.Now()
		unknown = unknownUIStrings(mainJS, rules)
		candidateElapsed := time.Since(candidateStart)
		logEvent("候选文案扫描: %d 条,耗时 %s", len(unknown), candidateElapsed)
		if versionChanged && state.PatchedClientVersion != "" && state.UnknownUIStrings != nil && candidateElapsed <= 200*time.Millisecond {
			result.NewCandidateCount = newStringCount(unknown, state.UnknownUIStrings)
			result.CandidateCountAvailable = true
		}
	}

	stage("应用翻译规则")
	t0 := time.Now()
	patchedMJS, hit := applyRules(mainJS, rules)
	result.RulesHit, result.RulesTotal, result.Replacements = reportStats(rules, hit)
	fmt.Printf("规则应用完成,耗时 %.1f 秒。\n", time.Since(t0).Seconds())

	stage("生成中文资源包")
	fmt.Println("正在生成本地资源包(临时文件)...")
	_ = os.Remove(tmpBundlePath)
	outFile, err := os.Create(tmpBundlePath)
	if err != nil {
		return fail(fmt.Sprintf("写入 web_bundle.zip 失败: %v", err))
	}
	zw := zip.NewWriter(outFile)
	writeErr := error(nil)
	for _, f := range inZf.File {
		hdr := &zip.FileHeader{
			Name:          f.Name,
			Method:        f.Method,
			Modified:      f.Modified,
			ExternalAttrs: f.ExternalAttrs,
			Comment:       f.Comment,
			NonUTF8:       f.NonUTF8,
		}
		wr, err := zw.CreateHeader(hdr)
		if err != nil {
			writeErr = err
			break
		}
		if f.Name == "main.js" {
			if _, err := wr.Write([]byte(patchedMJS)); err != nil {
				writeErr = err
				break
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			writeErr = err
			break
		}
		if _, err := io.Copy(wr, rc); err != nil {
			rc.Close()
			writeErr = err
			break
		}
		rc.Close()
	}
	zwErr := zw.Close()
	outErr := outFile.Close()
	if writeErr != nil || zwErr != nil || outErr != nil {
		return fail(fmt.Sprintf("写入 web_bundle.zip 失败: %v%v%v", writeErr, zwErr, outErr))
	}

	// 3. 回读校验资源包
	if ok, _ := validateBundle(tmpBundlePath); !ok {
		return fail("web_bundle.zip 校验失败")
	}
	fmt.Println("资源包校验通过。")

	// 4. 在临时文件里配置 app.asar,完成回读校验后才替换正式文件。
	if !alreadyPatched {
		stage("写入中文配置")
		fmt.Println("正在准备 app.asar 中文配置(临时文件)...")
		lsCode = strings.Replace(lsCode, insertAfter, insertAfter+injectText, 1)
		filesData[lsScriptPath] = []byte(lsCode)
		_ = os.Remove(tmpAsarPath)
		if err := packAsar(header, filesData, tmpAsarPath); err != nil {
			return fail(fmt.Sprintf("app.asar 写入失败: %v", err))
		}

		// 回读校验注入结果,并确认其它文件内容未被改动。
		_, filesVerify, err := unpackAsar(tmpAsarPath)
		if err != nil {
			return fail("app.asar 注入回读校验失败")
		}
		if !strings.Contains(toValidUTF8(filesVerify[lsScriptPath]), patchMarker) {
			return fail("app.asar 注入回读校验失败")
		}
		if len(filesVerify) != len(filesData) {
			return fail("app.asar 回读条目数量不一致")
		}
		for name, want := range filesData {
			if !bytes.Equal(filesVerify[name], want) {
				return fail(fmt.Sprintf("app.asar 回读内容不一致: %s", name))
			}
		}
		fmt.Println("app.asar 临时文件校验通过。")
	} else {
		fmt.Println("app.asar 已处于中文化配置状态。")
		tmpAsarPath = ""
	}

	stage("安全写入中文资源")
	fmt.Println("正在原子替换客户端资源 ...")
	if err := commitPatchArtifacts(appAsar, tmpAsarPath, webBundleZip, tmpBundlePath); err != nil {
		return fail(fmt.Sprintf("写入客户端失败: %v", err))
	}
	tmpAsarPath = ""
	tmpBundlePath = ""
	fmt.Println("客户端资源写入成功。")

	dictHash, _ := sha256File(filepath.Join(exeDir, "translations_settings.json"))
	bundleHash, _ := sha256File(webBundleZip)
	if dictHash != "" && bundleHash != "" {
		if err := saveState(launcherState{
			PatchedClientVersion:  curVer,
			DictionarySHA256:      dictHash,
			BundleSHA256:          bundleHash,
			UnknownUIStrings:      unknown,
			LastNewCandidateCount: result.NewCandidateCount,
		}); err != nil {
			logEvent("保存汉化状态失败: %v", err)
		}
	}

	stage("完成")
	fmt.Println("")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("恭喜!Antigravity 设置界面中文化完成!")
	fmt.Println("请重启客户端后按 Ctrl+, 查看效果。")
	fmt.Println("如需还原:重新运行本工具并选择「2. 还原官方英文」(或 --restore)。")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("")
	if result.CandidateCountAvailable && result.NewCandidateCount > 0 {
		fmt.Printf("提示：新版中有 %d 条界面文案暂未汉化，已按原英文显示。\n", result.NewCandidateCount)
	}
	return result, true
}

func restoreBackup(appDir string) bool {
	resources := filepath.Join(appDir, "resources")
	appAsar := filepath.Join(resources, "app.asar")
	appAsarBak := filepath.Join(resources, "app.asar.bak")
	bakMarker := appAsarBak + bakMarkerSuffix
	webBundleZip := filepath.Join(resources, "web_bundle.zip")
	restoreTmp := appAsar + fmt.Sprintf(".cn-restore-%d", os.Getpid())
	defer os.Remove(restoreTmp)

	curVer := getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
	if curVer == "" {
		curVer = "未知"
	}
	bakVer := ""
	if b, err := os.ReadFile(bakMarker); err == nil {
		bakVer = strings.TrimSpace(string(b))
	}

	reversedOK := false
	newAsar := ""
	if fileExists(appAsar) {
		if header, files, err := unpackAsar(appAsar); err != nil {
			fmt.Printf("警告:解析 app.asar 失败(%v),将尝试备份还原。\n", err)
		} else if ls, ok := files[lsScriptPath]; ok {
			code := toValidUTF8(ls)
			if strings.Contains(code, patchMarker) {
				if strings.Contains(code, injectText) {
					fmt.Println("正在逆向移除界面加载注入(不依赖备份)...")
					files[lsScriptPath] = []byte(strings.Replace(code, injectText, "", 1))
					_ = os.Remove(restoreTmp)
					if err := packAsar(header, files, restoreTmp); err != nil {
						fmt.Printf("警告:写回 app.asar 失败(%v),将尝试备份还原。\n", err)
					} else {
						_, verifyFiles, err := unpackAsar(restoreTmp)
						if err != nil {
							fmt.Printf("警告:还原文件回读失败(%v),将尝试备份还原。\n", err)
						} else if len(verifyFiles) != len(files) {
							fmt.Println("警告:还原文件条目数量不一致,将尝试备份还原。")
						} else {
							valid := true
							for name, want := range files {
								if !bytes.Equal(verifyFiles[name], want) {
									valid = false
									break
								}
							}
							if valid && !strings.Contains(toValidUTF8(verifyFiles[lsScriptPath]), patchMarker) {
								newAsar = restoreTmp
								reversedOK = true
							} else {
								fmt.Println("警告:还原文件校验未通过,将尝试备份还原。")
							}
						}
					}
				} else {
					fmt.Println("警告:检测到注入标记但文本不匹配,将改用备份还原。")
				}
			}
		}
	}

	restoredFromBackup := false
	if !reversedOK && fileExists(appAsarBak) {
		if bakVer != "" && curVer != "未知" && bakVer != curVer {
			fmt.Printf("警告:官方备份版本(%s)与当前客户端(%s)不一致,已跳过备份覆盖。\n", bakVer, curVer)
		} else {
			fmt.Println("正在从官方备份准备 app.asar ...")
			_ = os.Remove(restoreTmp)
			if err := copyFile(appAsarBak, restoreTmp); err != nil {
				fmt.Printf("错误:准备还原失败: %v\n", err)
				return false
			}
			if _, files, err := unpackAsar(restoreTmp); err != nil {
				fmt.Printf("警告:官方备份无法解析(%v),已跳过备份覆盖。\n", err)
				_ = os.Remove(restoreTmp)
			} else if strings.Contains(toValidUTF8(files[lsScriptPath]), patchMarker) {
				fmt.Println("警告:官方备份包含补丁标记,已跳过备份覆盖。")
				_ = os.Remove(restoreTmp)
			} else {
				newAsar = restoreTmp
				restoredFromBackup = true
			}
		}
	}

	hadBundle := fileExists(webBundleZip)
	if newAsar == "" && !hadBundle {
		fmt.Println("未发现需要还原的内容(既没有注入标记,也没有可用官方备份或资源包)。")
		return true
	}

	if err := commitRestoreArtifacts(appAsar, newAsar, webBundleZip); err != nil {
		fmt.Printf("错误:还原失败: %v\n", err)
		return false
	}
	if hadBundle {
		fmt.Println("已移除 web_bundle.zip。")
	}

	if !reversedOK && !restoredFromBackup && hadBundle {
		// 注入文本无法逆向且没有可用备份时,删除资源包即可让客户端回退英文;
		// 但保留明确提示,避免用户误以为 asar 也已被清理。
		if fileExists(appAsar) {
			if _, files, err := unpackAsar(appAsar); err == nil {
				if strings.Contains(toValidUTF8(files[lsScriptPath]), patchMarker) {
					fmt.Println("提示:app.asar 仍保留注入标记,但资源包已移除,客户端会以英文启动。")
				}
			}
		}
	}

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("已成功还原为官方英文原版!")
	fmt.Println("重新打开 Antigravity 客户端即可恢复纯英文界面。")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("")
	return true
}
