// Antigravity 设置界面(Ctrl+,)一键中文化工具 —— Go 版引擎(仅标准库)。
//
// 与 patch_settings.py(Python 参考实现)保持输出逐字节一致:
// 同一份版本、同一份词典,打出的补丁内容必须完全相同(以 --verify 的 sha256 校验)。
package main

import (
	"archive/zip"
	"bufio"
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

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procConsoleList = kernel32.NewProc("GetConsoleProcessList")
)

func main() {
	// 控制台按 UTF-8 输出(失败可忽略)
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	os.Exit(run())
}

func run() int {
	var (
		apply     bool
		restore   bool
		scan      bool
		verify    bool
		forceMenu bool
		pathArg   string
	)
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--apply":
			apply = true
		case a == "--restore":
			restore = true
		case a == "--scan":
			scan = true
		case a == "--verify":
			verify = true
		case a == "--menu":
			forceMenu = true
		case a == "--path":
			if i+1 < len(args) {
				pathArg = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--path="):
			pathArg = strings.TrimPrefix(a, "--path=")
		case a == "-h" || a == "--help":
			printUsage()
			return 0
		default:
			fmt.Printf("未知参数: %s\n", a)
			printUsage()
			return 1
		}
	}

	// 双击启动(独占控制台)且未显式指定动作时,进入交互菜单;
	// 从终端 / 脚本调用(共享控制台)保持纯命令行行为,不打断自动化。
	interactive := consoleProcessCount() == 1
	menuMode := forceMenu || (interactive && !apply && !restore && !scan && !verify)

	if interactive {
		// 双击场景:退出前暂停,保证能看到完整输出
		defer func() {
			fmt.Println("")
			fmt.Print("按回车键退出...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}()
	}

	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	appDir := getAntigravityDir(pathArg)
	if appDir == "" {
		fmt.Println("未检测到 Antigravity 安装目录,请确认客户端已安装在默认路径或使用 --path 参数指定。")
		return 1
	}
	fmt.Printf("定位到 Antigravity 安装目录: %s\n", appDir)

	rules, err := loadRules(filepath.Join(exeDir, "translations_settings.json"))
	if err != nil {
		fmt.Printf("错误:%v\n", err)
		return 1
	}

	if menuMode {
		return runMenu(appDir, exeDir, rules)
	}

	if scan {
		if err := doScan(appDir, exeDir, rules); err != nil {
			fmt.Printf("错误:%v\n", err)
			return 1
		}
		return 0
	}
	if verify {
		if err := doVerify(appDir, rules); err != nil {
			fmt.Printf("错误:%v\n", err)
			return 1
		}
		return 0
	}

	if isAntigravityRunning() {
		fmt.Println("提示:检测到 Antigravity 正在运行。建议在执行完后重启客户端查看最新效果。")
		fmt.Println("")
	}

	if restore {
		restoreBackup(appDir)
	} else {
		applyPatch(appDir, rules)
	}
	return 0
}

// consoleProcessCount 返回附加到当前控制台的进程数:
// 0 = 无控制台;1 = 独占(双击启动);>=2 = 共享(终端 / 脚本调用)。
func consoleProcessCount() int {
	var buf [4]uint32
	n, _, _ := procConsoleList.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return int(n)
}

// runMenu 双击启动时的交互菜单。
func runMenu(appDir, exeDir string, rules []*Rule) int {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("")
	for {
		fmt.Println("=============================================")
		fmt.Println("   Antigravity 设置界面中文化工具")
		fmt.Println("=============================================")
		fmt.Println("   1. 应用中文补丁")
		fmt.Println("   2. 还原官方英文")
		fmt.Println("   3. 兼容性扫描(适配新版)")
		fmt.Println("   4. 干跑验证")
		fmt.Println("   0. 退出")
		fmt.Println("=============================================")
		fmt.Print("请选择 [0-4]: ")
		line, err := reader.ReadString('\n')
		// 容错:管道输入可能携带 UTF-8 BOM(PowerShell 等场景)
		choice := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if err != nil && choice == "" {
			fmt.Println("")
			return 0
		}
		switch choice {
		case "1":
			fmt.Println("")
			applyPatch(appDir, rules)
		case "2":
			fmt.Println("")
			restoreBackup(appDir)
		case "3":
			fmt.Println("")
			if err := doScan(appDir, exeDir, rules); err != nil {
				fmt.Printf("错误:%v\n", err)
			}
		case "4":
			fmt.Println("")
			if err := doVerify(appDir, rules); err != nil {
				fmt.Printf("错误:%v\n", err)
			}
		case "0", "q", "Q":
			fmt.Println("")
			return 0
		default:
			fmt.Println("无效选项,请输入 0-4。")
			fmt.Println("")
			continue
		}
		fmt.Println("")
		fmt.Print("按回车键返回菜单...")
		if _, err := reader.ReadString('\n'); err != nil {
			return 0
		}
		fmt.Println("")
	}
}

func printUsage() {
	fmt.Println("用法: antigravity-chinese.exe [选项]")
	fmt.Println("  (无选项)    应用中文补丁;双击运行时显示交互菜单")
	fmt.Println("  --apply     应用中文补丁(显式指定,供脚本使用)")
	fmt.Println("  --restore   还原为官方英文(优先逆向移除注入,不依赖备份)")
	fmt.Println("  --scan      兼容性扫描:列出失效规则与未汉化新增文案")
	fmt.Println("  --verify    干跑验证:只统计规则命中情况,不写入客户端")
	fmt.Println("  --menu      强制显示交互菜单")
	fmt.Println("  --path DIR  手动指定 Antigravity 安装目录")
}

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

func applyPatch(appDir string, rules []*Rule) bool {
	resources := filepath.Join(appDir, "resources")
	appAsar := filepath.Join(resources, "app.asar")
	appAsarBak := filepath.Join(resources, "app.asar.bak")
	bakMarker := appAsarBak + bakMarkerSuffix
	lsExe := filepath.Join(resources, "bin", "language_server.exe")
	webBundleZip := filepath.Join(resources, "web_bundle.zip")

	if !fileExists(appAsar) || !fileExists(lsExe) {
		fmt.Printf("错误:未能找到 Antigravity 必要核心文件:\n%s\n%s\n", appAsar, lsExe)
		return false
	}

	curVer := getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
	if curVer == "" {
		curVer = "未知"
	}
	fmt.Printf("客户端版本: %s\n", curVer)

	// 打包自检:官方 asar 原样"解包 -> 重打包"必须与官方文件逐字节一致。
	// 若不一致说明 asar 结构假设有误(如数据区对齐),立即中止,绝不修改客户端。
	fmt.Println("正在进行 asar 打包自检(解包 -> 重打包 必须与官方逐字节一致)...")
	headerCheck, filesCheck, err := unpackAsar(appAsar)
	if err != nil {
		fmt.Printf("错误:asar 解析失败: %v\n", err)
		fmt.Println("已中止,客户端未被修改。请运行 --scan 检查结构变化或反馈此问题。")
		return false
	}
	tmpAsar := appAsar + ".selftest.tmp"
	if err := packAsar(headerCheck, filesCheck, tmpAsar); err != nil {
		os.Remove(tmpAsar)
		fmt.Printf("错误:asar 重打包失败: %v\n", err)
		return false
	}
	roundtrip, err1 := os.ReadFile(tmpAsar)
	original, err2 := os.ReadFile(appAsar)
	os.Remove(tmpAsar)
	if err1 != nil || err2 != nil || !bytes.Equal(roundtrip, original) {
		fmt.Println("错误:asar 打包自检未通过(重打包结果与官方不一致)!")
		fmt.Println("已中止,客户端未被修改。请运行 --scan 检查结构变化或反馈此问题。")
		return false
	}
	fmt.Println("打包自检通过。")

	header, filesData, err := unpackAsar(appAsar)
	if err != nil {
		fmt.Printf("错误:asar 解析失败: %v\n", err)
		return false
	}
	lsRaw, ok := filesData[lsScriptPath]
	if !ok {
		fmt.Printf("错误:app.asar 中未找到 %s\n", lsScriptPath)
		return false
	}
	lsCode := toValidUTF8(lsRaw)
	alreadyPatched := strings.Contains(lsCode, patchMarker)

	// 1. 备份官方原版(带版本标记,避免过期备份覆盖新版本)
	if !fileExists(appAsarBak) {
		fmt.Println("创建官方原版备份: app.asar.bak ...")
		if err := refreshBackup(appAsar, appAsarBak, bakMarker, curVer); err != nil {
			fmt.Printf("错误:创建备份失败: %v\n", err)
			return false
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
					fmt.Printf("错误:刷新备份失败: %v\n", err)
					return false
				}
			} else {
				fmt.Printf("提示:现有备份来自其他版本(%s),可能已过期;还原将优先采用逆向移除,不使用该备份。\n", orUnknown(bakVer))
			}
		} else {
			fmt.Println("已存在官方原版备份: app.asar.bak")
		}
	}

	// 2. 生成中文界面资源包
	bundleBytes, err := extractWebBundle(lsExe)
	if err != nil {
		fmt.Printf("错误:%v\n", err)
		return false
	}
	inZf, err := zip.NewReader(bytes.NewReader(bundleBytes), int64(len(bundleBytes)))
	if err != nil {
		fmt.Printf("错误:资源包解析失败: %v\n", err)
		return false
	}

	fmt.Println("正在汉化 main.js 设置界面文案...")
	var mainJS string
	for _, f := range inZf.File {
		if f.Name == "main.js" {
			rc, err := f.Open()
			if err != nil {
				fmt.Printf("错误:读取 main.js 失败: %v\n", err)
				return false
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				fmt.Printf("错误:读取 main.js 失败: %v\n", err)
				return false
			}
			mainJS = toValidUTF8(data)
		}
	}
	if mainJS == "" {
		fmt.Println("错误:资源包中未找到 main.js")
		return false
	}

	t0 := time.Now()
	patchedMJS, hit := applyRules(mainJS, rules)
	reportStats(rules, hit)
	fmt.Printf("规则应用完成,耗时 %.1f 秒。\n", time.Since(t0).Seconds())

	fmt.Println("正在生成本地资源包: web_bundle.zip ...")
	outFile, err := os.Create(webBundleZip)
	if err != nil {
		fmt.Printf("错误:写入 web_bundle.zip 失败: %v\n", err)
		return false
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
		fmt.Printf("错误:写入 web_bundle.zip 失败: %v%v%v\n", writeErr, zwErr, outErr)
		return false
	}

	// 3. 回读校验资源包
	if zr, err := zip.OpenReader(webBundleZip); err != nil {
		fmt.Printf("错误:web_bundle.zip 校验失败: %v\n", err)
		return false
	} else {
		found, size := false, uint64(0)
		for _, f := range zr.File {
			if f.Name == "main.js" {
				found, size = true, f.UncompressedSize64
			}
		}
		zr.Close()
		if !found || size == 0 {
			fmt.Println("错误:web_bundle.zip 校验失败!")
			return false
		}
	}
	fmt.Println("资源包校验通过。")

	// 4. 配置 app.asar 启用自定义界面资源包
	if !alreadyPatched {
		fmt.Println("正在配置 app.asar 加载中文界面...")
		if !strings.Contains(lsCode, insertAfter) {
			fmt.Println("错误:未在 app.asar 中找到注入锚点,客户端结构可能已变化!")
			fmt.Println("建议运行: antigravity-chinese.exe --scan 检查。")
			return false
		}
		lsCode = strings.Replace(lsCode, insertAfter, insertAfter+injectText, 1)
		filesData[lsScriptPath] = []byte(lsCode)
		if err := packAsar(header, filesData, appAsar); err != nil {
			fmt.Printf("错误:app.asar 写入失败: %v\n", err)
			return false
		}

		// 回读校验注入结果
		_, filesVerify, err := unpackAsar(appAsar)
		if err != nil {
			fmt.Println("错误:app.asar 注入回读校验失败!")
			return false
		}
		if !strings.Contains(toValidUTF8(filesVerify[lsScriptPath]), patchMarker) {
			fmt.Println("错误:app.asar 注入回读校验失败!")
			return false
		}
		fmt.Println("app.asar 配置成功(回读校验通过)。")
	} else {
		fmt.Println("app.asar 已处于中文化配置状态。")
	}

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("恭喜!Antigravity 设置界面中文化完成!")
	fmt.Println("请重启客户端后按 Ctrl+, 查看效果。")
	fmt.Println("如需还原:重新运行本工具并选择「2. 还原官方英文」(或 --restore)。")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("")
	return true
}

func restoreBackup(appDir string) bool {
	resources := filepath.Join(appDir, "resources")
	appAsar := filepath.Join(resources, "app.asar")
	appAsarBak := filepath.Join(resources, "app.asar.bak")
	bakMarker := appAsarBak + bakMarkerSuffix
	webBundleZip := filepath.Join(resources, "web_bundle.zip")

	reversedOK := false
	if fileExists(appAsar) {
		if header, files, err := unpackAsar(appAsar); err != nil {
			fmt.Printf("警告:解析 app.asar 失败(%v),将尝试备份还原。\n", err)
		} else if ls, ok := files[lsScriptPath]; ok {
			code := toValidUTF8(ls)
			if strings.Contains(code, patchMarker) {
				if strings.Contains(code, injectText) {
					fmt.Println("正在逆向移除界面加载注入(不依赖备份)...")
					files[lsScriptPath] = []byte(strings.Replace(code, injectText, "", 1))
					if err := packAsar(header, files, appAsar); err != nil {
						fmt.Printf("警告:写回 app.asar 失败(%v),将尝试备份还原。\n", err)
					} else {
						reversedOK = true
					}
				} else {
					fmt.Println("警告:检测到注入标记但文本不匹配,将改用备份还原。")
				}
			}
		}
	}

	if fileExists(webBundleZip) {
		os.Remove(webBundleZip)
		fmt.Println("已移除 web_bundle.zip。")
	}

	restored := false
	if !reversedOK && fileExists(appAsarBak) {
		fmt.Println("正在从官方备份还原 app.asar ...")
		if err := copyFile(appAsarBak, appAsar); err != nil {
			fmt.Printf("错误:还原失败: %v\n", err)
			return false
		}
		restored = true
		curVer := getFileVersion(filepath.Join(appDir, "Antigravity.exe"))
		if curVer == "" {
			curVer = "未知"
		}
		if b, err := os.ReadFile(bakMarker); err == nil {
			bakVer := strings.TrimSpace(string(b))
			if bakVer != "" && curVer != "未知" && bakVer != curVer {
				fmt.Printf("警告:备份版本(%s)与当前客户端版本(%s)不一致,请留意。\n", bakVer, curVer)
			}
		}
	}

	if !reversedOK && !restored {
		fmt.Println("未发现需要还原的内容(既没有注入标记,也没有官方备份)。")
		return true
	}

	fmt.Println("")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("已成功还原为官方英文原版!")
	fmt.Println("重新打开 Antigravity 客户端即可恢复纯英文界面。")
	fmt.Println(strings.Repeat("=", 45))
	fmt.Println("")
	return true
}
