//go:build launcher

package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmCommand        = 0x0111
	wmSetFont        = 0x0030
	wmCtlColorStatic = 0x0138
	wmAppUpdate      = 0x8001
	wmAppClose       = 0x8002

	wsChild       = 0x40000000
	wsVisible     = 0x10000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsTabStop     = 0x00010000
	bsPushButton  = 0x00000000

	ssLeft      = 0x00000000
	ssNoPrefix  = 0x00000080
	pbsSmooth   = 0x00000001
	pbmSetPos   = 0x0402
	pbmSetRange = 0x0406

	swHide = 0
	swShow = 5

	colorWindow          = 5
	idcArrow             = 32512
	defaultGUIFont       = 17
	setBkModeTransparent = 1

	pbClass     = "msctls_progress32"
	staticClass = "STATIC"
	buttonClass = "BUTTON"
	windowClass = "AntigravityCNLauncherWindow"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostQuitMessage    = user32.NewProc("PostQuitMessage")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procShowWindow         = user32.NewProc("ShowWindow")
	procUpdateWindow       = user32.NewProc("UpdateWindow")
	procSetWindowTextW     = user32.NewProc("SetWindowTextW")
	procSendMessageW       = user32.NewProc("SendMessageW")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procGetModuleHandleW   = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	procLoadCursorW        = user32.NewProc("LoadCursorW")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procGetSysColorBrush   = user32.NewProc("GetSysColorBrush")
	procInitCommonControls = comctl32.NewProc("InitCommonControlsEx")
	procGetStockObject     = gdi32.NewProc("GetStockObject")
	procSetBkMode          = gdi32.NewProc("SetBkMode")
	procSetTextColor       = gdi32.NewProc("SetTextColor")
)

type point struct {
	X int32
	Y int32
}

type winMsg struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

type initCommonControlsEx struct {
	Size uint32
	ICC  uint32
}

type buttonSpec struct {
	ID   int
	Text string
}

type uiState struct {
	Title        string
	Detail       string
	Progress     int
	ShowProgress bool
	Busy         bool
	CanClose     bool
	CloseAction  int
	Buttons      []buttonSpec
}

type launcherUI struct {
	mu sync.Mutex

	hwnd      syscall.Handle
	hTitle    syscall.Handle
	hDetail   syscall.Handle
	hProgress syscall.Handle
	buttons   [3]syscall.Handle

	state     uiState
	actions   [3]int
	choiceCh  chan int
	closed    chan struct{}
	closeOnce sync.Once
	exitCode  int
}

var activeLauncherUI *launcherUI

func newLauncherUI() (*launcherUI, error) {
	procSetProcessDPIAware.Call()
	ui := &launcherUI{
		choiceCh: make(chan int, 4),
		closed:   make(chan struct{}),
		state: uiState{
			Title:    "Antigravity CN",
			Detail:   "正在准备…",
			Progress: 0,
			Busy:     true,
		},
	}
	activeLauncherUI = ui
	icc := initCommonControlsEx{Size: uint32(unsafe.Sizeof(initCommonControlsEx{})), ICC: 0x20}
	procInitCommonControls.Call(uintptr(unsafe.Pointer(&icc)))

	instance, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	className, _ := syscall.UTF16PtrFromString(windowClass)
	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:    syscall.NewCallback(launcherWndProc),
		Instance:   syscall.Handle(instance),
		Cursor:     syscall.Handle(cursor),
		Background: syscall.Handle(colorWindow + 1),
		ClassName:  className,
	}
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return nil, fmt.Errorf("注册启动器窗口失败: %v", err)
	}

	title, _ := syscall.UTF16PtrFromString("Antigravity CN 启动器")
	style := uintptr(wsCaption | wsSysMenu | wsMinimizeBox)
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		style,
		0x80000000, // CW_USEDEFAULT
		0x80000000,
		580,
		330,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("创建启动器窗口失败: %v", err)
	}
	ui.hwnd = syscall.Handle(hwnd)

	ui.hTitle = createControl(staticClass, "", wsChild|wsVisible|ssLeft|ssNoPrefix, 24, 18, 530, 30, 0, instance)
	ui.hDetail = createControl(staticClass, "", wsChild|wsVisible|ssLeft|ssNoPrefix, 24, 54, 530, 142, 0, instance)
	ui.hProgress = createControl(pbClass, "", wsChild|wsVisible|pbsSmooth, 24, 204, 530, 16, 0, instance)
	for i := 0; i < 3; i++ {
		ui.buttons[i] = createControl(buttonClass, "", wsChild|wsVisible|wsTabStop|bsPushButton, 24, 242, 160, 32, 1001+i, instance)
	}
	font, _, _ := procGetStockObject.Call(defaultGUIFont)
	for _, h := range []syscall.Handle{ui.hTitle, ui.hDetail, ui.hProgress, ui.buttons[0], ui.buttons[1], ui.buttons[2]} {
		sendMessage(h, wmSetFont, font, 1)
	}
	sendMessage(ui.hProgress, pbmSetRange, 0, 100)
	procShowWindow.Call(uintptr(ui.hwnd), swShow)
	procUpdateWindow.Call(uintptr(ui.hwnd))
	ui.applyState()
	return ui, nil
}

func createControl(class, text string, style uintptr, x, y, w, h int, id int, instance uintptr) syscall.Handle {
	cls, _ := syscall.UTF16PtrFromString(class)
	txt, _ := syscall.UTF16PtrFromString(text)
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(txt)),
		style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(activeLauncherUI.hwnd),
		uintptr(id),
		instance,
		0,
	)
	return syscall.Handle(hwnd)
}

func sendMessage(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procSendMessageW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func setText(hwnd syscall.Handle, text string) {
	p, _ := syscall.UTF16PtrFromString(text)
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(p)))
}

func (ui *launcherUI) set(s uiState) {
	ui.mu.Lock()
	ui.state = s
	ui.mu.Unlock()
	procPostMessageW.Call(uintptr(ui.hwnd), wmAppUpdate, 0, 0)
}

func (ui *launcherUI) showBusy(title, detail string, progress int, canClose bool) {
	ui.set(uiState{
		Title: title, Detail: detail, Progress: progress,
		ShowProgress: progress >= 0, Busy: !canClose, CanClose: canClose,
		CloseAction: 0,
	})
}

func (ui *launcherUI) ask(title, detail string, buttons []buttonSpec, closeAction int) int {
	// 清掉上一个窗口残留的快速点击,避免自动选择。
	for {
		select {
		case <-ui.choiceCh:
			continue
		default:
		}
		break
	}
	ui.set(uiState{
		Title: title, Detail: detail, Progress: 0, ShowProgress: false,
		Busy: false, CanClose: true, CloseAction: closeAction, Buttons: buttons,
	})
	for {
		select {
		case id := <-ui.choiceCh:
			return id
		case <-ui.closed:
			return closeAction
		}
	}
}

func (ui *launcherUI) finish(code int) {
	ui.mu.Lock()
	ui.exitCode = code
	ui.state.CanClose = true
	ui.state.Busy = false
	ui.mu.Unlock()
	procPostMessageW.Call(uintptr(ui.hwnd), wmAppClose, 0, 0)
}

func (ui *launcherUI) close() {
	ui.finish(0)
}

func (ui *launcherUI) applyState() {
	ui.mu.Lock()
	s := ui.state
	ui.mu.Unlock()

	setText(ui.hTitle, s.Title)
	setText(ui.hDetail, s.Detail)
	if s.ShowProgress {
		procShowWindow.Call(uintptr(ui.hProgress), swShow)
		if s.Progress < 0 {
			s.Progress = 0
		}
		if s.Progress > 100 {
			s.Progress = 100
		}
		sendMessage(ui.hProgress, pbmSetPos, uintptr(s.Progress), 0)
	} else {
		procShowWindow.Call(uintptr(ui.hProgress), swHide)
	}

	ui.mu.Lock()
	for i := 0; i < 3; i++ {
		if i < len(s.Buttons) {
			setText(ui.buttons[i], s.Buttons[i].Text)
			procShowWindow.Call(uintptr(ui.buttons[i]), swShow)
			ui.actions[i] = s.Buttons[i].ID
			width, gap := 160, 12
			total := len(s.Buttons)*width + (len(s.Buttons)-1)*gap
			x := (580 - total) / 2
			x += i * (width + gap)
			procSetWindowPos.Call(uintptr(ui.buttons[i]), 0, uintptr(x), 242, uintptr(width), 32, 0x0014)
		} else {
			procShowWindow.Call(uintptr(ui.buttons[i]), swHide)
			ui.actions[i] = 0
		}
	}
	ui.mu.Unlock()
}

func (ui *launcherUI) run() {
	var msg winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 || ret == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	ui.closeOnce.Do(func() { close(ui.closed) })
}

func launcherWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	ui := activeLauncherUI
	if ui == nil {
		ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return ret
	}
	switch msg {
	case wmAppUpdate:
		ui.applyState()
		return 0
	case wmAppClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmCommand:
		id := int(wParam & 0xffff)
		index := id - 1001
		ui.mu.Lock()
		action := 0
		if index >= 0 && index < len(ui.actions) {
			action = ui.actions[index]
		}
		ui.mu.Unlock()
		if action != 0 {
			select {
			case ui.choiceCh <- action:
			default:
			}
		}
		return 0
	case wmClose:
		ui.mu.Lock()
		canClose := ui.state.CanClose
		closeAction := ui.state.CloseAction
		busy := ui.state.Busy
		ui.mu.Unlock()
		if !canClose || busy {
			return 0
		}
		if closeAction > 0 {
			select {
			case ui.choiceCh <- closeAction:
			default:
			}
		}
		return 0
	case wmCtlColorStatic:
		procSetBkMode.Call(wParam, setBkModeTransparent)
		procSetTextColor.Call(wParam, 0)
		brush, _, _ := procGetSysColorBrush.Call(colorWindow)
		return brush
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}
