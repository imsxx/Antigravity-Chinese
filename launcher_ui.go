//go:build launcher

package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	stdInputHandle  uintptr = 0xFFFFFFF6
	stdOutputHandle uintptr = 0xFFFFFFF5

	enableProcessedInput          = 0x0001
	enableLineInput               = 0x0002
	enableEchoInput               = 0x0004
	enableVirtualTerminalInput    = 0x0200
	enableVirtualTerminalProgress = 0x0004

	keyEventType = 0x0001
	vkEscape     = 0x1B
	vkReturn     = 0x0D
	vkUp         = 0x26
	vkDown       = 0x28
	vkHome       = 0x24
	vkEnd        = 0x23

	spiGetWorkArea = 0x0030
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpShowWindow  = 0x0040
	swRestore      = 9
	hwndTopMost    = ^uintptr(0)

	launcherConsoleTitle = "Antigravity CN 启动器"

	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[91m"
	ansiYellow = "\x1b[93m"
	ansiGreen  = "\x1b[92m"
	ansiCyan   = "\x1b[96m"
)

type buttonSpec struct {
	ID   int
	Text string
}

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type consoleKeyEvent struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

type consoleInputRecord struct {
	EventType uint16
	_         uint16
	KeyEvent  consoleKeyEvent
}

type launcherUI struct {
	mu sync.Mutex

	input       syscall.Handle
	output      syscall.Handle
	writer      io.Writer
	exitCode    int
	section     string
	dynamicLine bool
	vtEnabled   bool
	ownsConsole bool
}

var (
	consoleKernel32 = syscall.NewLazyDLL("kernel32.dll")
	consoleUser32   = syscall.NewLazyDLL("user32.dll")

	procGetStdHandle            = consoleKernel32.NewProc("GetStdHandle")
	procGetConsoleMode          = consoleKernel32.NewProc("GetConsoleMode")
	procSetConsoleMode          = consoleKernel32.NewProc("SetConsoleMode")
	procSetConsoleCP            = consoleKernel32.NewProc("SetConsoleCP")
	procSetConsoleOutputCP      = consoleKernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleTitleW        = consoleKernel32.NewProc("SetConsoleTitleW")
	procGetConsoleProcessList   = consoleKernel32.NewProc("GetConsoleProcessList")
	procFlushConsoleInputBuffer = consoleKernel32.NewProc("FlushConsoleInputBuffer")
	procReadConsoleInputW       = consoleKernel32.NewProc("ReadConsoleInputW")
	procEnumWindows             = consoleUser32.NewProc("EnumWindows")
	procGetWindowTextLengthW    = consoleUser32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW          = consoleUser32.NewProc("GetWindowTextW")
	procIsWindowVisible         = consoleUser32.NewProc("IsWindowVisible")
	procGetWindowRect           = consoleUser32.NewProc("GetWindowRect")
	procSetWindowPos            = consoleUser32.NewProc("SetWindowPos")
	procSystemParametersInfoW   = consoleUser32.NewProc("SystemParametersInfoW")
	procShowWindow              = consoleUser32.NewProc("ShowWindow")
	procSetForegroundWindow     = consoleUser32.NewProc("SetForegroundWindow")
)

func newLauncherUI() (*launcherUI, error) {
	procSetConsoleCP.Call(65001)
	procSetConsoleOutputCP.Call(65001)

	ui := &launcherUI{
		input:       consoleHandle(stdInputHandle),
		output:      consoleHandle(stdOutputHandle),
		writer:      os.Stdout,
		ownsConsole: consoleProcessCount() <= 1,
	}
	ui.vtEnabled = ui.enableVirtualTerminal()
	if ui.ownsConsole {
		setConsoleTitle(launcherConsoleTitle)
		// Windows Terminal/ConPTY 的主窗口不会立刻带控制台标题出现,异步重试直到找到再居中。
		go prepareConsoleWindow(launcherConsoleTitle)
	}

	ui.writeRaw(fmt.Sprintf("> Antigravity CN 启动器 %s\r\n> 正在准备…\r\n", toolVersion))
	if !ui.vtEnabled {
		ui.writeRaw("! 当前终端不支持上下键菜单，将使用编号选择。\r\n")
	}
	return ui, nil
}

func consoleHandle(kind uintptr) syscall.Handle {
	handle, _, _ := procGetStdHandle.Call(kind)
	return syscall.Handle(handle)
}

func consoleProcessCount() int {
	var pids [2]uint32
	count, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	return int(count)
}

func setConsoleTitle(title string) {
	value, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(value)))
}

func findVisibleWindowByTitle(title string) uintptr {
	var found uintptr
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		length, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if length == 0 {
			return 1
		}
		buf := make([]uint16, int(length)+1)
		procGetWindowTextW.Call(
			hwnd,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
		)
		if syscall.UTF16ToString(buf) == title {
			found = hwnd
			return 0
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	runtime.KeepAlive(callback)
	return found
}

func prepareConsoleWindow(title string) {
	for attempt := 0; attempt < 20; attempt++ {
		hwnd := findVisibleWindowByTitle(title)
		if centerConsoleWindow(hwnd) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func bringLauncherConsoleToFront() {
	for attempt := 0; attempt < 10; attempt++ {
		hwnd := findVisibleWindowByTitle(launcherConsoleTitle)
		if hwnd != 0 {
			procShowWindow.Call(hwnd, swRestore)
			procSetWindowPos.Call(
				hwnd,
				hwndTopMost,
				0,
				0,
				0,
				0,
				swpNoMove|swpNoSize|swpShowWindow,
			)
			procSetForegroundWindow.Call(hwnd)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func centerConsoleWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	procShowWindow.Call(hwnd, swRestore)
	var window winRect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&window))); ok == 0 {
		return false
	}
	width := window.Right - window.Left
	height := window.Bottom - window.Top
	if width <= 0 || height <= 0 {
		return false
	}

	var work winRect
	ok, _, _ := procSystemParametersInfoW.Call(
		spiGetWorkArea,
		0,
		uintptr(unsafe.Pointer(&work)),
		0,
	)
	if ok == 0 || work.Right <= work.Left || work.Bottom <= work.Top {
		return false
	}
	x, y := centeredConsolePosition(work, width, height)
	moved, _, _ := procSetWindowPos.Call(
		hwnd,
		0,
		uintptr(x),
		uintptr(y),
		0,
		0,
		swpNoSize|swpShowWindow,
	)
	if moved == 0 {
		return false
	}
	procSetForegroundWindow.Call(hwnd)
	return true
}

func centeredConsolePosition(work winRect, width, height int32) (int, int) {
	x := int(work.Left) + (int(work.Right-work.Left)-int(width))/2
	y := int(work.Top) + (int(work.Bottom-work.Top)-int(height))/2
	if x < int(work.Left) {
		x = int(work.Left)
	}
	if y < int(work.Top) {
		y = int(work.Top)
	}
	return x, y
}

func (ui *launcherUI) enableVirtualTerminal() bool {
	var mode uint32
	if ok, _, _ := procGetConsoleMode.Call(uintptr(ui.output), uintptr(unsafe.Pointer(&mode))); ok == 0 {
		return false
	}
	mode |= enableVirtualTerminalProgress
	ok, _, _ := procSetConsoleMode.Call(uintptr(ui.output), uintptr(mode))
	return ok != 0
}

func (ui *launcherUI) writeRaw(text string) {
	_, _ = io.WriteString(ui.writer, text)
}

func (ui *launcherUI) clearDynamicLocked() {
	if !ui.dynamicLine {
		return
	}
	ui.writeRaw("\r\n")
	ui.dynamicLine = false
}

func (ui *launcherUI) styleLocked(color, text string) string {
	if !ui.vtEnabled {
		return text
	}
	return color + text + ansiReset
}

func (ui *launcherUI) appendSectionLocked(title string) {
	if strings.TrimSpace(title) == "" || title == ui.section {
		return
	}
	if ui.section != "" {
		ui.writeRaw("\r\n")
	}
	ui.writeRaw(ui.styleLocked(ansiCyan, "["+title+"]") + "\r\n")
	ui.section = title
}

func detailLines(detail string) []string {
	detail = strings.ReplaceAll(detail, "\r\n", "\n")
	var lines []string
	for _, line := range strings.Split(detail, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (ui *launcherUI) showBusy(title, detail string, progress int, canClose bool) {
	_ = progress
	_ = canClose

	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearDynamicLocked()
	ui.appendSectionLocked(title)
	for _, line := range detailLines(detail) {
		ui.writeDetailLocked(line)
	}
}

func (ui *launcherUI) writeDetailLocked(line string) {
	color := detailColor(line, ui.section)
	text := "> " + line
	if color != "" {
		text = ui.styleLocked(color, text)
	}
	ui.writeRaw(text + "\r\n")
}

func isErrorText(text string) bool {
	return strings.HasPrefix(text, "错误：") ||
		strings.HasPrefix(text, "!") ||
		strings.Contains(text, "失败") ||
		strings.Contains(text, "异常") ||
		strings.Contains(text, "无法") ||
		strings.Contains(text, "问题")
}

func detailColor(line, section string) string {
	switch {
	case isErrorText(line):
		return ansiRed
	case strings.HasPrefix(line, "提示："), strings.HasPrefix(line, "警告："),
		strings.Contains(line, "跳过"), strings.Contains(line, "关闭"),
		strings.Contains(line, "未完成"):
		return ansiYellow
	case section == "运行完成":
		return ansiGreen
	default:
		return ""
	}
}

func (ui *launcherUI) warn(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearDynamicLocked()
	ui.writeRaw(ui.styleLocked(ansiYellow, "! "+text) + "\r\n")
}

func (ui *launcherUI) reportError(text string) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.clearDynamicLocked()
	ui.writeRaw(ui.styleLocked(ansiRed, "错误："+text) + "\r\n")
}

// updateProgress 覆盖当前动态行,用于下载进度,不会刷掉已经完成的步骤。
func (ui *launcherUI) updateProgress(title, detail string, progress int) {
	_ = progress

	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.appendSectionLocked(title)
	if ui.vtEnabled {
		ui.writeRaw("\r\x1b[2K> " + detail)
	} else {
		ui.writeRaw("\r> " + detail)
	}
	ui.dynamicLine = true
}

func (ui *launcherUI) showReady(title, detail string) {
	ui.showBusy(title, detail, 100, true)
}

func (ui *launcherUI) exitCodeValue() int {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.exitCode
}

func (ui *launcherUI) finish(code int) {
	ui.mu.Lock()
	ui.exitCode = code
	ui.clearDynamicLocked()
	ui.mu.Unlock()
}

func (ui *launcherUI) countdownAndFinish(seconds int) {
	ui.countdown("运行完成，将在 %d 秒后自动关闭本窗口...", seconds)
	ui.mu.Lock()
	if ui.vtEnabled {
		ui.writeRaw("\r\x1b[2K" + ui.styleLocked(ansiGreen, "运行完成，窗口即将关闭。") + "\r\n")
	} else {
		ui.writeRaw("\r运行完成，窗口即将关闭。\r\n")
	}
	ui.mu.Unlock()
	ui.finish(0)
}

func (ui *launcherUI) countdown(format string, seconds int) {
	if seconds < 1 {
		seconds = 1
	}
	ui.mu.Lock()
	ui.clearDynamicLocked()
	ui.writeRaw("\r\n")
	ui.mu.Unlock()

	for remaining := seconds; remaining >= 1; remaining-- {
		ui.mu.Lock()
		line := fmt.Sprintf(format, remaining)
		if ui.vtEnabled {
			ui.writeRaw("\r\x1b[2K" + ui.styleLocked(ansiYellow, line))
		} else {
			ui.writeRaw("\r" + line)
		}
		ui.mu.Unlock()
		time.Sleep(time.Second)
	}

	ui.mu.Lock()
	if ui.vtEnabled {
		ui.writeRaw("\r\x1b[2K")
	} else {
		ui.writeRaw("\r")
	}
	ui.mu.Unlock()
}

func (ui *launcherUI) reportFatal(text string, code int) {
	ui.reportError(text)
	if ui.ownsConsole {
		ui.countdown("启动器无法继续，将在 %d 秒后关闭本窗口...", 3)
	}
	ui.finish(code)
}

func menuSelection(current, delta, count int) int {
	if count <= 0 {
		return 0
	}
	current = (current + delta) % count
	if current < 0 {
		current += count
	}
	return current
}

func selectionForCloseAction(buttons []buttonSpec, closeAction int) int {
	for i, button := range buttons {
		if button.ID == closeAction {
			return i
		}
	}
	if len(buttons) == 0 {
		return 0
	}
	return len(buttons) - 1
}

func (ui *launcherUI) ask(title, detail string, buttons []buttonSpec, closeAction int) int {
	ui.mu.Lock()
	ui.clearDynamicLocked()
	titleColor := ansiYellow
	if isErrorText(title) {
		titleColor = ansiRed
	}
	ui.writeRaw("\r\n" + ui.styleLocked(titleColor, "? "+title) + "\r\n")
	for _, line := range detailLines(detail) {
		text := "  " + line
		if color := detailColor(line, ""); color != "" {
			text = ui.styleLocked(color, text)
		}
		ui.writeRaw(text + "\r\n")
	}
	ui.writeRaw("\r\n")
	ui.mu.Unlock()

	if len(buttons) == 0 {
		return closeAction
	}

	var selected int
	if ui.vtEnabled {
		ui.writeRaw("  使用 ↑/↓ 选择，Enter 确认，Esc 取消\r\n")
		ui.renderMenu(buttons, 0, false)
		selected = ui.readArrowChoice(buttons, closeAction)
		ui.renderMenu(buttons, selected, true)
	} else {
		selected = ui.readNumberChoice(buttons, closeAction)
	}
	ui.mu.Lock()
	ui.writeRaw("  → " + buttons[selected].Text + "\r\n")
	ui.mu.Unlock()
	return buttons[selected].ID
}

func (ui *launcherUI) renderMenu(buttons []buttonSpec, selected int, redraw bool) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if redraw && ui.vtEnabled {
		ui.writeRaw(fmt.Sprintf("\x1b[%dA", len(buttons)))
	}
	for i, button := range buttons {
		prefix := "    "
		style := ""
		reset := ""
		if i == selected {
			prefix = "  > "
			if ui.vtEnabled {
				style = "\x1b[7m"
				reset = "\x1b[0m"
			}
		}
		line := prefix + button.Text
		if ui.vtEnabled {
			ui.writeRaw("\r\x1b[2K" + style + line + reset + "\r\n")
		} else {
			ui.writeRaw(line + "\r\n")
		}
	}
}

func (ui *launcherUI) setRawInputMode() (uint32, bool) {
	var mode uint32
	if ok, _, _ := procGetConsoleMode.Call(uintptr(ui.input), uintptr(unsafe.Pointer(&mode))); ok == 0 {
		return 0, false
	}
	raw := mode &^ uint32(enableLineInput|enableEchoInput|enableVirtualTerminalInput)
	if ok, _, _ := procSetConsoleMode.Call(uintptr(ui.input), uintptr(raw)); ok == 0 {
		return 0, false
	}
	procFlushConsoleInputBuffer.Call(uintptr(ui.input))
	return mode, true
}

func (ui *launcherUI) restoreInputMode(mode uint32) {
	procSetConsoleMode.Call(uintptr(ui.input), uintptr(mode))
	procFlushConsoleInputBuffer.Call(uintptr(ui.input))
}

func (ui *launcherUI) readArrowChoice(buttons []buttonSpec, closeAction int) int {
	originalMode, ok := ui.setRawInputMode()
	if !ok {
		return ui.readNumberChoice(buttons, closeAction)
	}
	defer ui.restoreInputMode(originalMode)

	selected := 0
	for {
		var record consoleInputRecord
		var read uint32
		ok, _, _ := procReadConsoleInputW.Call(
			uintptr(ui.input),
			uintptr(unsafe.Pointer(&record)),
			1,
			uintptr(unsafe.Pointer(&read)),
		)
		if ok == 0 || read == 0 {
			return selected
		}
		if record.EventType != keyEventType || record.KeyEvent.KeyDown == 0 {
			continue
		}
		switch record.KeyEvent.VirtualKeyCode {
		case vkUp:
			selected = menuSelection(selected, -1, len(buttons))
			ui.renderMenu(buttons, selected, true)
		case vkDown:
			selected = menuSelection(selected, 1, len(buttons))
			ui.renderMenu(buttons, selected, true)
		case vkHome:
			selected = 0
			ui.renderMenu(buttons, selected, true)
		case vkEnd:
			selected = len(buttons) - 1
			ui.renderMenu(buttons, selected, true)
		case vkEscape:
			return selectionForCloseAction(buttons, closeAction)
		case vkReturn:
			return selected
		}
	}
}

func (ui *launcherUI) readNumberChoice(buttons []buttonSpec, closeAction int) int {
	selected := selectionForCloseAction(buttons, closeAction)
	for i, button := range buttons {
		ui.writeRaw(fmt.Sprintf("  %d. %s\r\n", i+1, button.Text))
	}
	ui.writeRaw(fmt.Sprintf("  请选择 [1-%d]，直接回车取消: ", len(buttons)))

	var choice int
	if _, err := fmt.Fscan(os.Stdin, &choice); err != nil || choice < 1 || choice > len(buttons) {
		return selected
	}
	return choice - 1
}
