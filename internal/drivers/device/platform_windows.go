package device

import (
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var openClipboard = user32.NewProc("OpenClipboard")
var closeClipboard = user32.NewProc("CloseClipboard")
var getClipboard = user32.NewProc("GetClipboardData")
var setClipboard = user32.NewProc("SetClipboardData")
var emptyClipboard = user32.NewProc("EmptyClipboard")
var countClipboardFormats = user32.NewProc("CountClipboardFormats")
var globalLock = kernel32.NewProc("GlobalLock")
var globalUnlock = kernel32.NewProc("GlobalUnlock")
var globalAlloc = kernel32.NewProc("GlobalAlloc")
var globalFree = kernel32.NewProc("GlobalFree")
var globalSize = kernel32.NewProc("GlobalSize")
var moveMemory = kernel32.NewProc("RtlMoveMemory")
var createWindow = user32.NewProc("CreateWindowExW")
var destroyWindow = user32.NewProc("DestroyWindow")

func hideCommand(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func ReadClipboard() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ok, _, _ := openClipboard.Call(0)
	if ok == 0 {
		return "", errors.New("device: cannot open Windows clipboard")
	}
	defer closeClipboard.Call()
	h, _, _ := getClipboard.Call(13)
	if h == 0 {
		count, _, _ := countClipboardFormats.Call()
		if count == 0 {
			return "", nil
		}
		return "", errors.New("device: clipboard must contain Unicode text before use")
	}
	size, _, _ := globalSize.Call(h)
	if size < 2 || size > 2<<20 {
		return "", errors.New("device: invalid clipboard size")
	}
	ptr, _, _ := globalLock.Call(h)
	if ptr == 0 {
		return "", errors.New("device: cannot lock clipboard")
	}
	defer globalUnlock.Call(h)
	chars := make([]uint16, int(size/2))
	moveMemory.Call(uintptr(unsafe.Pointer(&chars[0])), ptr, uintptr(len(chars)*2))
	runtime.KeepAlive(chars)
	for i, c := range chars {
		if c == 0 {
			return string(utf16.Decode(chars[:i])), nil
		}
	}
	return "", errors.New("device: unterminated clipboard")
}
func WriteClipboard(text string) error {
	chars, e := windows.UTF16FromString(text)
	if e != nil {
		return errors.New("device: clipboard text contains NUL")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		return errors.New("device: cannot create clipboard owner")
	}
	defer destroyWindow.Call(hwnd)
	ok, _, _ := openClipboard.Call(hwnd)
	if ok == 0 {
		return errors.New("device: cannot open Windows clipboard")
	}
	defer closeClipboard.Call()
	h, _, _ := globalAlloc.Call(2, uintptr(len(chars)*2))
	if h == 0 {
		return errors.New("device: clipboard allocation failed")
	}
	owned := true
	defer func() {
		if owned {
			globalFree.Call(h)
		}
	}()
	ptr, _, _ := globalLock.Call(h)
	if ptr == 0 {
		return errors.New("device: cannot lock clipboard allocation")
	}
	moveMemory.Call(ptr, uintptr(unsafe.Pointer(&chars[0])), uintptr(len(chars)*2))
	runtime.KeepAlive(chars)
	globalUnlock.Call(h)
	if ok, _, _ := emptyClipboard.Call(); ok == 0 {
		return errors.New("device: cannot empty clipboard")
	}
	if ok, _, _ := setClipboard.Call(13, h); ok == 0 {
		return errors.New("device: cannot set clipboard")
	}
	owned = false
	return nil
}
