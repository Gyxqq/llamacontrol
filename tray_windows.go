//go:build windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	trayID          = 1
	trayCallbackMsg = 0x0400 + 1

	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	wmCommand       = 0x0111
	wmDestroy       = 0x0002
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	mfString    = 0x00000000
	tpmRightBtn = 0x0002

	cmdShow = 1001
	cmdQuit = 1002
)

var (
	trayOnce sync.Once

	user32              = windows.NewLazySystemDLL("user32.dll")
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassEx = user32.NewProc("RegisterClassExW")
	procCreateWindowEx  = user32.NewProc("CreateWindowExW")
	procDefWindowProc   = user32.NewProc("DefWindowProcW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
	procPostQuitMessage = user32.NewProc("PostQuitMessage")
	procGetMessage      = user32.NewProc("GetMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMsg     = user32.NewProc("DispatchMessageW")
	procLoadImage       = user32.NewProc("LoadImageW")
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procAppendMenu      = user32.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32.NewProc("DestroyMenu")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    windows.Handle
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
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type notifyIconData struct {
	Size             uint32
	Hwnd             windows.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GuidItem         windows.GUID
	BalloonIcon      windows.Handle
}

func (a *App) startTray() {
	trayOnce.Do(func() {
		go runTray(a)
	})
}

func runTray(app *App) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	className, _ := windows.UTF16PtrFromString("llamacontrolTrayWindow")
	instanceRaw, _, _ := procGetModuleHandle.Call(0)
	instance := windows.Handle(instanceRaw)
	wndProc := syscall.NewCallback(func(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case trayCallbackMsg:
			switch uint32(lParam) {
			case wmRButtonUp:
				showTrayMenu(hwnd, app)
				return 0
			case wmLButtonDblClk:
				app.ShowMainWindow()
				return 0
			}
		case wmCommand:
			switch uint16(wParam & 0xffff) {
			case cmdShow:
				app.ShowMainWindow()
				return 0
			case cmdQuit:
				removeTrayIcon(hwnd)
				app.QuitApp()
				procDestroyWindow.Call(uintptr(hwnd))
				return 0
			}
		case wmDestroy:
			removeTrayIcon(hwnd)
			procPostQuitMessage.Call(0)
			return 0
		}

		ret, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return ret
	})

	wc := wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   wndProc,
		Instance:  instance,
		ClassName: className,
	}
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwndRaw, _, err := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0, 0, 0, 0,
		0, 0,
		uintptr(instance),
		0,
	)
	if hwndRaw == 0 {
		log.Warnf("tray: failed to create hidden window: %v", err)
		return
	}

	hwnd := windows.Handle(hwndRaw)
	addTrayIcon(hwnd)

	var message msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func addTrayIcon(hwnd windows.Handle) {
	nid := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            hwnd,
		ID:              trayID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayCallbackMsg,
	}
	copy(nid.Tip[:], windows.StringToUTF16("llamacontrol"))

	if iconPath := trayIconPath(); iconPath != "" {
		pathPtr, _ := windows.UTF16PtrFromString(iconPath)
		icon, _, _ := procLoadImage.Call(0, uintptr(unsafe.Pointer(pathPtr)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
		nid.Icon = windows.Handle(icon)
	}

	if nid.Icon == 0 {
		log.Warn("tray: icon file not found; using Windows default icon slot")
	}

	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func removeTrayIcon(hwnd windows.Handle) {
	nid := notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd: hwnd,
		ID:   trayID,
	}
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func showTrayMenu(hwnd windows.Handle, app *App) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	showText, _ := windows.UTF16PtrFromString("显示主窗口")
	quitText, _ := windows.UTF16PtrFromString("退出")
	procAppendMenu.Call(menu, mfString, cmdShow, uintptr(unsafe.Pointer(showText)))
	procAppendMenu.Call(menu, mfString, cmdQuit, uintptr(unsafe.Pointer(quitText)))

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForeground.Call(uintptr(hwnd))
	procTrackPopupMenu.Call(menu, tpmRightBtn, uintptr(cursor.X), uintptr(cursor.Y), 0, uintptr(hwnd), 0)
}

func trayIconPath() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "build", "windows", "icon.ico")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
	}

	candidate := filepath.Join("build", "windows", "icon.ico")
	if _, err := os.Stat(candidate); err == nil {
		abs, absErr := filepath.Abs(candidate)
		if absErr == nil {
			return abs
		}
		return candidate
	}

	return ""
}
