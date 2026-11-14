package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	ole32                = syscall.NewLazyDLL("ole32.dll")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSHGetFolderPathW = shell32.NewProc("SHGetFolderPathW")

	clsidShellLink = comGUID{
		Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidIShellLinkW = comGUID{
		Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidIPersistFile = comGUID{
		Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

const (
	clsctxInprocServer      = 0x1
	coinitApartmentThreaded = 0x2
	csidlDesktopDirectory   = 0x0010
	rpcEChangedMode         = 0x80010106
	maxPath                 = 260
)

func ensureDesktopShortcut(exeDir string) error {
	return createShortcutForDir(exeDir, false)
}

func createDesktopShortcut(exeDir string) error {
	return createShortcutForDir(exeDir, true)
}

func createShortcutForDir(exeDir string, force bool) error {
	target := filepath.Join(exeDir, "antigravity-cn.exe")
	if st, err := os.Stat(target); err != nil || st.IsDir() {
		return fmt.Errorf("未找到 Antigravity CN 启动器: %s", target)
	}
	link, err := desktopShortcutPath()
	if err != nil {
		return err
	}
	if !force && fileExists(link) {
		return nil
	}
	if err := createShortcutAt(link, target, exeDir, "Antigravity 中文启动器"); err != nil {
		return fmt.Errorf("创建快捷方式失败: %w", err)
	}
	return nil
}

func desktopShortcutPath() (string, error) {
	var buf [maxPath]uint16
	hr, _, err := procSHGetFolderPathW.Call(
		0,
		csidlDesktopDirectory,
		0,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if int32(uint32(hr)) < 0 {
		return "", fmt.Errorf("读取桌面目录失败: %v", err)
	}
	desktop := syscall.UTF16ToString(buf[:])
	if desktop == "" {
		return "", fmt.Errorf("桌面目录为空")
	}
	return filepath.Join(desktop, "Antigravity CN.lnk"), nil
}

func createShortcutAt(linkPath, target, workDir, description string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if int32(uint32(hr)) < 0 {
		if uint32(hr) == rpcEChangedMode {
			return fmt.Errorf("COM 已以其它线程模型初始化")
		}
		return fmt.Errorf("初始化 COM 失败: 0x%08x", uint32(hr))
	}
	defer procCoUninitialize.Call()

	var link unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if comFailed(hr) || link == nil {
		return fmt.Errorf("创建 ShellLink 失败: 0x%08x", uint32(hr))
	}
	defer comCall(link, 2) // Release

	target16, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	work16, err := syscall.UTF16PtrFromString(workDir)
	if err != nil {
		return err
	}
	desc16, err := syscall.UTF16PtrFromString(description)
	if err != nil {
		return err
	}
	if comFailed(comCall(link, 20, uintptr(unsafe.Pointer(target16)))) { // SetPath
		return fmt.Errorf("设置快捷方式目标失败")
	}
	if comFailed(comCall(link, 9, uintptr(unsafe.Pointer(work16)))) { // SetWorkingDirectory
		return fmt.Errorf("设置快捷方式工作目录失败")
	}
	if comFailed(comCall(link, 7, uintptr(unsafe.Pointer(desc16)))) { // SetDescription
		return fmt.Errorf("设置快捷方式描述失败")
	}
	// 使用目标程序自身的图标,不引用任何官方标识资源。
	_ = comCall(link, 17, uintptr(unsafe.Pointer(target16)), 0) // SetIconLocation

	var persist unsafe.Pointer
	hr = comCall(link, 0,
		uintptr(unsafe.Pointer(&iidIPersistFile)),
		uintptr(unsafe.Pointer(&persist)),
	)
	if comFailed(hr) || persist == nil {
		return fmt.Errorf("获取 IPersistFile 失败: 0x%08x", uint32(hr))
	}
	defer comCall(persist, 2) // Release

	link16, err := syscall.UTF16PtrFromString(linkPath)
	if err != nil {
		return err
	}
	if comFailed(comCall(persist, 6, uintptr(unsafe.Pointer(link16)), 1)) { // Save
		return fmt.Errorf("保存快捷方式失败")
	}
	return nil
}

func comCall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	method := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	callArgs := make([]uintptr, 0, len(args)+1)
	callArgs = append(callArgs, uintptr(obj))
	callArgs = append(callArgs, args...)
	ret, _, _ := syscall.SyscallN(method, callArgs...)
	return ret
}

func comFailed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}
