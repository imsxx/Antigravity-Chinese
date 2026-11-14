package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	patchKernel32   = syscall.NewLazyDLL("kernel32.dll")
	procMoveFileExW = patchKernel32.NewProc("MoveFileExW")
)

type patchArtifacts struct {
	appAsar      string
	bundle       string
	appBak       string
	bundleBak    string
	hadBundle    bool
	removeBundle bool
}

// commitPatchArtifacts 在全部新文件生成并校验后,才替换客户端资源。
// 任一步失败都会尝试恢复操作前的 asar 与资源包,避免留下半汉化状态。
func commitPatchArtifacts(appAsar, newAsar, webBundle, newBundle string) error {
	return commitClientArtifacts(appAsar, newAsar, webBundle, newBundle, false)
}

func commitRestoreArtifacts(appAsar, newAsar, webBundle string) error {
	return commitClientArtifacts(appAsar, newAsar, webBundle, "", true)
}

func commitClientArtifacts(appAsar, newAsar, webBundle, newBundle string, removeBundle bool) error {
	if newAsar == "" && newBundle == "" {
		if !removeBundle {
			return nil
		}
	}

	id := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	tx := patchArtifacts{
		appAsar:      appAsar,
		bundle:       webBundle,
		appBak:       appAsar + ".cn-tx-" + id,
		bundleBak:    webBundle + ".cn-tx-" + id,
		hadBundle:    fileExists(webBundle),
		removeBundle: removeBundle,
	}
	cleanup := func() {
		_ = os.Remove(tx.appBak)
		_ = os.Remove(tx.bundleBak)
		if newAsar != "" {
			_ = os.Remove(newAsar)
		}
		if newBundle != "" {
			_ = os.Remove(newBundle)
		}
	}

	rollback := func(cause error) error {
		var rollbackErrs []string
		if newAsar != "" {
			if err := restoreFromBackup(tx.appBak, tx.appAsar); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("恢复 app.asar: %v", err))
			}
		}
		if newBundle != "" || removeBundle {
			if tx.hadBundle {
				if err := restoreFromBackup(tx.bundleBak, tx.bundle); err != nil {
					rollbackErrs = append(rollbackErrs, fmt.Sprintf("恢复 web_bundle.zip: %v", err))
				}
			} else if err := os.Remove(tx.bundle); err != nil && !os.IsNotExist(err) {
				rollbackErrs = append(rollbackErrs, fmt.Sprintf("移除 web_bundle.zip: %v", err))
			}
		}
		cleanup()
		if len(rollbackErrs) > 0 {
			return fmt.Errorf("%w; 回滚不完整: %s", cause, strings.Join(rollbackErrs, "; "))
		}
		return cause
	}

	if newAsar != "" {
		if err := copyFile(tx.appAsar, tx.appBak); err != nil {
			cleanup()
			return fmt.Errorf("创建 asar 事务备份失败: %w", err)
		}
	}
	if (newBundle != "" || removeBundle) && tx.hadBundle {
		if err := copyFile(tx.bundle, tx.bundleBak); err != nil {
			cleanup()
			return fmt.Errorf("创建资源包事务备份失败: %w", err)
		}
	}

	if newAsar != "" {
		if err := replaceFileWithTemp(newAsar, tx.appAsar); err != nil {
			return rollback(fmt.Errorf("写入 app.asar 失败: %w", err))
		}
	}
	if newBundle != "" {
		if err := replaceFileWithTemp(newBundle, tx.bundle); err != nil {
			return rollback(fmt.Errorf("写入 web_bundle.zip 失败: %w", err))
		}
	} else if removeBundle {
		if err := os.Remove(tx.bundle); err != nil && !os.IsNotExist(err) {
			return rollback(fmt.Errorf("移除 web_bundle.zip 失败: %w", err))
		}
	}

	if newAsar != "" {
		_, files, err := unpackAsar(tx.appAsar)
		if err != nil {
			return rollback(fmt.Errorf("app.asar 写入后校验失败: %w", err))
		}
		hasMarker := strings.Contains(toValidUTF8(files[lsScriptPath]), patchMarker)
		if !removeBundle && !hasMarker {
			return rollback(fmt.Errorf("app.asar 写入后缺少补丁标记"))
		}
		if removeBundle && hasMarker {
			return rollback(fmt.Errorf("app.asar 还原后仍包含补丁标记"))
		}
	}
	if newBundle != "" {
		if ok, _ := validateBundle(tx.bundle); !ok {
			return rollback(fmt.Errorf("web_bundle.zip 写入后校验失败"))
		}
	}
	if removeBundle && fileExists(tx.bundle) {
		return rollback(fmt.Errorf("web_bundle.zip 移除后仍存在"))
	}

	cleanup()
	return nil
}

func replaceFileWithTemp(src, dst string) error {
	src16, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	dst16, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	const moveFileReplaceExisting = 0x1
	const moveFileWriteThrough = 0x8
	ok, _, callErr := procMoveFileExW.Call(
		uintptr(unsafe.Pointer(src16)),
		uintptr(unsafe.Pointer(dst16)),
		moveFileReplaceExisting|moveFileWriteThrough,
	)
	if ok != 0 {
		return nil
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
		return fmt.Errorf("替换文件失败: %w", errno)
	}
	return fmt.Errorf("替换文件失败: %s", dst)
}

func restoreFromBackup(backup, dst string) error {
	if !fileExists(backup) {
		return fmt.Errorf("事务备份不存在")
	}
	tmp := dst + ".cn-restore.tmp"
	_ = os.Remove(tmp)
	if err := copyFile(backup, tmp); err != nil {
		return err
	}
	if err := replaceFileWithTemp(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
