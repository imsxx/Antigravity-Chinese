//go:build !launcher

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procConsoleList = kernel32.NewProc("GetConsoleProcessList")
)

func main() {
	kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	os.Exit(run())
}

func run() int {
	var (
		apply        bool
		restore      bool
		scan         bool
		verify       bool
		status       bool
		jsonOutput   bool
		forceMenu    bool
		makeShortcut bool
		enableCheck  bool
		disableCheck bool
		showVersion  bool
		pathArg      string
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
		case a == "--status":
			status = true
		case a == "--json":
			jsonOutput = true
		case a == "--menu":
			forceMenu = true
		case a == "--create-shortcut":
			makeShortcut = true
		case a == "--enable-update-check":
			enableCheck = true
		case a == "--disable-update-check":
			disableCheck = true
		case a == "--version":
			showVersion = true
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

	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	if showVersion {
		fmt.Printf("Antigravity-Chinese %s\n", toolVersion)
		return 0
	}

	if enableCheck || disableCheck {
		cfg := loadConfig()
		cfg.UpdateCheckEnabled = enableCheck
		if err := saveConfig(cfg); err != nil {
			fmt.Printf("错误:%v\n", err)
			return 1
		}
		if cfg.UpdateCheckEnabled {
			fmt.Println("已开启自动更新检查。")
		} else {
			fmt.Println("已关闭自动更新检查。")
		}
		return 0
	}
	if makeShortcut {
		if err := createDesktopShortcut(exeDir); err != nil {
			fmt.Printf("错误:%v\n", err)
			return 1
		}
		fmt.Println("已创建/修复桌面快捷方式: Antigravity CN")
		return 0
	}

	appDir := getAntigravityDir(pathArg)
	if status {
		st := inspectPatchStatus(appDir, exeDir)
		if jsonOutput {
			data, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Printf("客户端版本: %s\n", orUnknown(st.ClientVersion))
			fmt.Printf("安装目录: %s\n", orUnknown(st.AppDir))
			fmt.Printf("补丁标记: %s\n", yesNo(st.Patched))
			fmt.Printf("资源包有效: %s\n", yesNo(st.BundleValid))
			fmt.Printf("状态文件匹配: %s\n", yesNo(st.StateMatches))
			fmt.Printf("需要汉化: %s\n", yesNo(st.NeedsPatch))
			if st.Error != "" {
				fmt.Printf("错误: %s\n", st.Error)
			}
		}
		if st.Error != "" {
			return 1
		}
		if st.NeedsPatch {
			return 3
		}
		return 0
	}

	// 双击启动(独占控制台)且未显式指定动作时,进入交互菜单;
	// 从终端 / 脚本调用(共享控制台)保持纯命令行行为,不打断自动化。
	interactive := consoleProcessCount() == 1
	action := apply || restore || scan || verify
	menuMode := forceMenu || (interactive && !action)

	if interactive {
		defer func() {
			fmt.Println("")
			fmt.Print("按回车键退出...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}()
	}

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
		if !restoreBackup(appDir) {
			return 1
		}
		removeState()
		return 0
	}
	result, ok := applyPatchWithProgress(appDir, exeDir, rules, nil)
	if !ok {
		return 1
	}
	fmt.Printf("汉化状态已记录: 客户端 %s, %d 条规则, %d 处替换\n",
		orUnknown(result.ClientVersion), result.RulesHit, result.Replacements)
	if err := ensureDesktopShortcut(exeDir); err != nil {
		logEvent("创建桌面快捷方式失败: %v", err)
	}
	return 0
}

func consoleProcessCount() int {
	var buf [4]uint32
	n, _, _ := procConsoleList.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return int(n)
}

func runMenu(appDir, exeDir string, rules []*Rule) int {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("")
	for {
		cfg := loadConfig()
		updateText := "重新开启自动更新检查"
		if cfg.UpdateCheckEnabled {
			updateText = "关闭自动更新检查"
		}
		fmt.Println("=============================================")
		fmt.Println("   Antigravity 设置界面中文化工具")
		fmt.Println("=============================================")
		fmt.Println("   1. 应用中文补丁")
		fmt.Println("   2. 还原官方英文")
		fmt.Println("   3. 兼容性扫描(适配新版)")
		fmt.Println("   4. 干跑验证")
		fmt.Println("   5. 创建/修复 Antigravity CN 桌面快捷方式")
		fmt.Printf("   6. %s\n", updateText)
		fmt.Println("   0. 退出")
		fmt.Println("=============================================")
		fmt.Print("请选择 [0-6]: ")
		line, err := reader.ReadString('\n')
		choice := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if err != nil && choice == "" {
			fmt.Println("")
			return 0
		}
		switch choice {
		case "1":
			fmt.Println("")
			if _, ok := applyPatchWithProgress(appDir, exeDir, rules, nil); !ok {
				fmt.Println("应用失败,详见上方日志。")
			} else if err := ensureDesktopShortcut(exeDir); err != nil {
				logEvent("创建桌面快捷方式失败: %v", err)
			}
		case "2":
			fmt.Println("")
			if restoreBackup(appDir) {
				removeState()
			}
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
		case "5":
			fmt.Println("")
			if err := createDesktopShortcut(exeDir); err != nil {
				fmt.Printf("错误:%v\n", err)
			} else {
				fmt.Println("已创建/修复桌面快捷方式: Antigravity CN")
			}
		case "6":
			cfg.UpdateCheckEnabled = !cfg.UpdateCheckEnabled
			if err := saveConfig(cfg); err != nil {
				fmt.Printf("错误:%v\n", err)
			} else if cfg.UpdateCheckEnabled {
				fmt.Println("已开启自动更新检查。")
			} else {
				fmt.Println("已关闭自动更新检查。")
			}
		case "0", "q", "Q":
			fmt.Println("")
			return 0
		default:
			fmt.Println("无效选项,请输入 0-6。")
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
	fmt.Println("  --status    输出补丁状态;配合 --json 输出机器可读结果")
	fmt.Println("  --json      将 --status 结果输出为 JSON")
	fmt.Println("  --create-shortcut  创建/修复 Antigravity CN 桌面快捷方式")
	fmt.Println("  --enable-update-check / --disable-update-check")
	fmt.Println("  --version   输出版本号")
	fmt.Println("  --menu      强制显示交互菜单")
	fmt.Println("  --path DIR  手动指定 Antigravity 安装目录")
	fmt.Println("")
	fmt.Println("--status 退出码: 0=已就绪, 3=需要汉化, 1=错误")
}
