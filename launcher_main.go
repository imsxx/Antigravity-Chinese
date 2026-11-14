//go:build launcher

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type launcherOptions struct {
	AppDir         string
	UpdatedVersion string
	SkipUpdate     bool
}

type localStatusResult struct {
	status patchStatus
	err    error
}

type updateCheckResult struct {
	release *toolRelease
	err     error
	note    string
}

var procCreateMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

const errorAlreadyExists = 183

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--apply-update" {
		os.Exit(applyUpdateMode(os.Args[2:]))
	}
	ui, err := newLauncherUI()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：无法初始化终端启动器:", err)
		os.Exit(1)
	}
	mutex, acquired := acquireLauncherMutex()
	if !acquired {
		ui.reportFatal("Antigravity CN 已在运行，请先关闭现有启动器。", 0)
		os.Exit(0)
	}
	if mutex != 0 {
		defer procCloseHandle.Call(uintptr(mutex))
	}
	opts, err := parseLauncherOptions(os.Args[1:])
	if err != nil {
		ui.reportFatal("启动参数错误："+err.Error(), 2)
		os.Exit(2)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				logEvent("启动器发生异常: %v", recovered)
				showPatchFailure(ui, "启动器发生异常", fmt.Sprintf("错误：%v\n\n本地补丁不会因此被修改。", recovered), "", false)
			}
		}()
		runLauncherWorkflow(ui, opts)
	}()

	code := ui.exitCodeValue()
	if code != 0 {
		os.Exit(code)
	}
}

func acquireLauncherMutex() (syscall.Handle, bool) {
	name, err := syscall.UTF16PtrFromString(`Local\Antigravity-Chinese-Launcher-v1`)
	if err != nil {
		return 0, true
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		logEvent("创建启动器互斥量失败: %v", callErr)
		return 0, true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		procCloseHandle.Call(handle)
		return 0, false
	}
	return syscall.Handle(handle), true
}

func parseLauncherOptions(args []string) (launcherOptions, error) {
	var opts launcherOptions
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--path":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--path 缺少目录")
			}
			opts.AppDir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--path="):
			opts.AppDir = strings.TrimPrefix(args[i], "--path=")
		case args[i] == "--updated":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--updated 缺少版本号")
			}
			opts.UpdatedVersion = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--updated="):
			opts.UpdatedVersion = strings.TrimPrefix(args[i], "--updated=")
		case args[i] == "--skip-update-once":
			opts.SkipUpdate = true
		default:
			return opts, fmt.Errorf("未知启动参数: %s", args[i])
		}
	}
	return opts, nil
}

func runLauncherWorkflow(ui *launcherUI, opts launcherOptions) {
	exeDir := "."
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	cfg := loadConfig()
	if pending := consumePendingUpdateError(); pending != "" {
		ui.warn("上次工具更新未完成：" + pending)
	}
	appDir := getAntigravityDir(opts.AppDir)
	ui.showBusy("正在检查启动环境", "正在检查本地补丁与工具更新…", 0, false)

	localCh := make(chan localStatusResult, 1)
	go func() {
		localCh <- localStatusResult{status: inspectPatchStatus(appDir, exeDir)}
	}()

	updateCh := make(chan updateCheckResult, 1)
	shouldCheck := cfg.UpdateCheckEnabled && toolVersion != "dev" && opts.UpdatedVersion == "" && !opts.SkipUpdate
	if shouldCheck {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client := &http.Client{Timeout: 2 * time.Second}
			release, err := fetchLatestRelease(ctx, repoAPIURL, client)
			if err != nil {
				updateCh <- updateCheckResult{err: err, note: "更新检查未完成，继续使用本地版本"}
				return
			}
			if !isNewerToolVersion(toolVersion, release.Tag) {
				updateCh <- updateCheckResult{note: "工具已是最新版本 " + toolVersion}
				return
			}
			if release.ZipURL == "" || release.ChecksumsURL == "" {
				updateCh <- updateCheckResult{err: fmt.Errorf("最新版本发布资源不完整"), note: "更新检查未完成，继续使用本地版本"}
				return
			}
			updateCh <- updateCheckResult{release: release}
		}()
	} else {
		switch {
		case opts.UpdatedVersion != "":
			updateCh <- updateCheckResult{note: "已更新至 " + opts.UpdatedVersion}
		case !cfg.UpdateCheckEnabled:
			updateCh <- updateCheckResult{note: "更新检查已关闭"}
		case toolVersion == "dev":
			updateCh <- updateCheckResult{note: "开发版跳过更新检查"}
		default:
			updateCh <- updateCheckResult{note: "本次已跳过工具更新"}
		}
	}

	local := <-localCh
	if local.err != nil {
		logEvent("本地状态检查失败: %v", local.err)
	}
	localDetail := describeLocalStatus(local.status)
	ui.showBusy("正在检查启动环境", localDetail, 18, false)
	ui.showBusy("正在检查启动环境", "正在等待工具更新检查…", 18, false)
	updateResult := <-updateCh
	updateNote := updateResult.note
	if updateResult.err != nil {
		logEvent("更新检查失败: %v", updateResult.err)
	}

	if updateResult.release != nil {
		choice, updated := promptAndApplyUpdate(ui, exeDir, updateResult.release)
		switch choice {
		case updateChoiceExit:
			ui.finish(1)
			return
		case updateChoiceDisabled:
			cfg.UpdateCheckEnabled = false
			updateNote = "更新检查已关闭"
		case updateChoiceSkipped:
			updateNote = "本次已跳过工具更新"
		case updateChoiceUpdated:
			if updated {
				ui.showBusy("更新已准备完成", "正在重启 Antigravity CN 并继续检查补丁…", 100, false)
				time.Sleep(600 * time.Millisecond)
				ui.finish(0)
				return
			}
		}
	}
	if strings.TrimSpace(updateNote) != "" {
		ui.showBusy("正在检查启动环境", updateNote+"。", 28, false)
	}

	if local.status.Error != "" {
		logEvent("客户端状态异常: %s", local.status.Error)
		detail := local.status.Error
		if appDir != "" {
			detail += "\n\n可以尝试以英文启动原客户端，或打开日志查看详情。"
		}
		showPatchFailure(ui, "无法准备中文补丁", detail, appDir, appDir != "")
		return
	}

	result, patched := (applyResult{}), false
	if local.status.NeedsPatch {
		if !waitForAntigravityClosed(ui) {
			ui.finish(0)
			return
		}
		if isAntigravityRunning() {
			showPatchFailure(ui, "客户端仍在运行", "Antigravity 尚未完全退出，汉化已取消。\n\n请从托盘菜单彻底退出后，再从 Antigravity CN 启动。", appDir, true)
			return
		}
		rules, err := loadRules(filepath.Join(exeDir, "translations_settings.json"))
		if err != nil {
			showPatchFailure(ui, "无法应用中文补丁", err.Error(), appDir, true)
			return
		}
		ui.showBusy("正在应用中文补丁", "正在校验客户端结构…", 5, false)
		result, patched = applyPatchWithProgress(appDir, exeDir, rules, func(stage string) {
			ui.showBusy("正在应用中文补丁", stage+"…", patchStagePercent(stage), false)
		})
		if !patched {
			showPatchFailure(ui, "汉化失败，客户端保持原样",
				result.Error+"\n\n没有写入未通过校验的客户端资源。", appDir, true)
			return
		}
		if err := ensureDesktopShortcut(exeDir); err != nil {
			logEvent("创建桌面快捷方式失败: %v", err)
		}
	} else {
		if err := ensureDesktopShortcut(exeDir); err != nil {
			logEvent("创建桌面快捷方式失败: %v", err)
		}
	}

	detailLines := []string{}
	if patched {
		detailLines = append(detailLines, fmt.Sprintf("补丁已就绪：客户端 %s，命中 %d/%d 条规则。",
			orUnknown(result.ClientVersion), result.RulesHit, result.RulesTotal))
	} else {
		detailLines = append(detailLines, "补丁已就绪，当前界面资源有效。")
	}
	if result.CandidateCountAvailable && result.NewCandidateCount > 0 {
		detailLines = append(detailLines, fmt.Sprintf(
			"提示：新版中有 %d 条界面文案暂未汉化，已按原英文显示。",
			result.NewCandidateCount,
		))
	}
	detailLines = append(detailLines, "正在启动 Antigravity…")
	ui.showBusy("正在启动", strings.Join(detailLines, "\n"), 100, false)
	time.Sleep(180 * time.Millisecond)
	if err := launchAntigravity(appDir); err != nil {
		logEvent("启动 Antigravity 失败: %v", err)
		showLaunchFailure(ui, err.Error())
		return
	}
	showLaunchedState(ui, "运行完成", "Antigravity 已启动，中文环境已就绪。")
}

func describeLocalStatus(st patchStatus) string {
	if st.AppDir == "" {
		return "本地状态：未检测到 Antigravity 安装目录"
	}
	version := orUnknown(st.ClientVersion)
	if st.Error != "" {
		return fmt.Sprintf("本地状态：客户端 %s，检查出现问题：%s", version, st.Error)
	}
	if st.NeedsPatch {
		return fmt.Sprintf("本地状态：客户端 %s，需要更新中文补丁", version)
	}
	return fmt.Sprintf("本地状态：客户端 %s，中文补丁已就绪", version)
}

func showLaunchedState(ui *launcherUI, title, detail string) {
	ui.showReady(title, detail)
	if ui.ownsConsole {
		bringLauncherConsoleToFront()
	}
	ui.countdownAndFinish(3)
}

const (
	updateChoiceRetry = iota + 1
	updateChoiceSkipped
	updateChoiceDisabled
	updateChoiceExit
	updateChoiceUpdated
)

func promptAndApplyUpdate(ui *launcherUI, exeDir string, rel *toolRelease) (choice int, updated bool) {
	for {
		choice = ui.ask(
			"发现新版本 "+rel.Tag,
			fmt.Sprintf("当前工具版本：%s\n新版本：%s\n\n更新只替换汉化工具本身，不会改动当前客户端。", toolVersion, rel.Tag),
			[]buttonSpec{
				{ID: updateChoiceRetry, Text: "立即更新"},
				{ID: updateChoiceSkipped, Text: "不更新启动"},
				{ID: updateChoiceDisabled, Text: "关闭更新检查"},
			},
			updateChoiceSkipped,
		)
		switch choice {
		case updateChoiceSkipped:
			return updateChoiceSkipped, false
		case updateChoiceDisabled:
			cfg := loadConfig()
			cfg.UpdateCheckEnabled = false
			if err := saveConfig(cfg); err != nil {
				logEvent("保存更新检查设置失败: %v", err)
			}
			return updateChoiceDisabled, false
		case updateChoiceExit:
			return updateChoiceExit, false
		case updateChoiceRetry:
			client := &http.Client{Timeout: 10 * time.Minute}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			staged, err := prepareToolUpdate(ctx, rel, client, func(stage string, pct int) {
				if strings.Contains(stage, "MB") {
					ui.updateProgress("正在更新汉化工具", stage, pct)
				} else {
					ui.showBusy("正在更新汉化工具", stage, pct, false)
				}
			})
			cancel()
			if err == nil {
				ui.showBusy("正在更新汉化工具", "正在准备安全替换并重启…", 98, false)
				err = spawnUpdateHelper(exeDir, staged, rel.Tag)
			}
			if err == nil {
				logEvent("工具更新已准备完成: %s", rel.Tag)
				return updateChoiceUpdated, true
			}
			logEvent("工具更新失败: %v", err)
			retryChoice := ui.ask(
				"更新失败",
				err.Error()+"\n\n旧版本与客户端均未被破坏，可以重试。",
				[]buttonSpec{
					{ID: updateChoiceRetry, Text: "重试更新"},
					{ID: updateChoiceSkipped, Text: "跳过并启动"},
					{ID: updateChoiceExit, Text: "退出"},
				},
				updateChoiceExit,
			)
			switch retryChoice {
			case updateChoiceRetry:
				continue
			case updateChoiceSkipped:
				return updateChoiceSkipped, false
			default:
				return updateChoiceExit, false
			}
		}
	}
}

func patchStagePercent(stage string) int {
	switch stage {
	case "校验客户端结构":
		return 10
	case "读取界面资源":
		return 24
	case "分析新增文案":
		return 38
	case "应用翻译规则":
		return 54
	case "生成中文资源包":
		return 70
	case "写入中文配置":
		return 82
	case "安全写入中文资源":
		return 91
	case "完成":
		return 98
	default:
		return 15
	}
}

func waitForAntigravityClosed(ui *launcherUI) bool {
	for {
		if !isAntigravityRunning() {
			return true
		}
		choice := ui.ask(
			"Antigravity 正在运行",
			"汉化需要先完全退出客户端（包括托盘进程）。\n\n本工具不会强制结束进程，请关闭后点击“关闭并重试”。",
			[]buttonSpec{
				{ID: 1, Text: "关闭并重试"},
				{ID: 2, Text: "取消"},
			},
			2,
		)
		if choice == 2 {
			return false
		}
		time.Sleep(600 * time.Millisecond)
	}
}

func showPatchFailure(ui *launcherUI, title, detail, appDir string, allowEnglish bool) {
	for {
		buttons := []buttonSpec{}
		if allowEnglish {
			buttons = append(buttons, buttonSpec{ID: 1, Text: "以英文启动"})
		}
		logID, exitID := 2, 3
		if !allowEnglish {
			logID, exitID = 1, 2
		}
		buttons = append(buttons,
			buttonSpec{ID: logID, Text: "打开日志"},
			buttonSpec{ID: exitID, Text: "退出"},
		)
		choice := ui.ask(title, detail, buttons, exitID)
		if allowEnglish {
			switch choice {
			case 1:
				if err := launchAntigravity(appDir); err != nil {
					detail += "\n\n以英文启动失败：" + err.Error()
					continue
				}
				showLaunchedState(ui, "运行完成", "Antigravity 已以英文启动。")
				return
			case 2:
				if err := openLauncherLog(); err != nil {
					detail += "\n\n打开日志失败：" + err.Error()
				}
			default:
				ui.finish(1)
				return
			}
		} else {
			switch choice {
			case 1:
				if err := openLauncherLog(); err != nil {
					detail += "\n\n打开日志失败：" + err.Error()
				}
			default:
				ui.finish(1)
				return
			}
		}
	}
}

func showLaunchFailure(ui *launcherUI, detail string) {
	for {
		choice := ui.ask(
			"无法启动 Antigravity",
			detail+"\n\n客户端文件没有被修改，可以直接打开日志检查。",
			[]buttonSpec{
				{ID: 1, Text: "打开日志"},
				{ID: 2, Text: "退出"},
			},
			2,
		)
		if choice == 1 {
			if err := openLauncherLog(); err != nil {
				detail += "\n\n打开日志失败：" + err.Error()
				continue
			}
			continue
		}
		ui.finish(1)
		return
	}
}

func openLauncherLog() error {
	if _, err := ensureAppDataDir(); err != nil {
		return err
	}
	if !fileExists(logPath()) {
		if err := os.WriteFile(logPath(), []byte("暂无日志。\n"), 0o644); err != nil {
			return err
		}
	}
	cmd := exec.Command("notepad.exe", logPath())
	return cmd.Start()
}

func launchAntigravity(appDir string) error {
	exe := filepath.Join(appDir, "Antigravity.exe")
	if !fileExists(exe) {
		return fmt.Errorf("未找到 %s", exe)
	}
	cmd := exec.Command(exe)
	cmd.Dir = appDir
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
