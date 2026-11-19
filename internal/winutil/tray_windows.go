package winutil

import (
	"runtime"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmDestroy     = 0x0002
	wmCommand     = 0x0111
	wmUser        = 0x0400
	wmTrayIcon    = wmUser + 1
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	mfString      = 0x00000000
	mfChecked     = 0x00000008
	mfSeparator   = 0x00000800
	tpmRightBtn   = 0x0002
	tpmBottomAlign = 0x0020

	cmdOpenDashboard = 1001
	cmdSendFiles     = 1002
	cmdPushClipboard = 1003
	cmdOpenFolder    = 1004
	cmdToggleAuto    = 1005
	cmdToggleSendTo  = 1006
	cmdQuit          = 1099
)

var (
	user32Tray   = windows.NewLazySystemDLL("user32.dll")
	shell32Tray  = windows.NewLazySystemDLL("shell32.dll")
	kernel32Tray = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW      = user32Tray.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32Tray.NewProc("CreateWindowExW")
	procDestroyWindow         = user32Tray.NewProc("DestroyWindow")
	procDefWindowProcW        = user32Tray.NewProc("DefWindowProcW")
	procGetMessageW           = user32Tray.NewProc("GetMessageW")
	procTranslateMessage      = user32Tray.NewProc("TranslateMessage")
	procDispatchMessageW      = user32Tray.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32Tray.NewProc("PostQuitMessage")
	procPostMessageW          = user32Tray.NewProc("PostMessageW")
	procLoadImageW            = user32Tray.NewProc("LoadImageW")
	procDestroyIcon           = user32Tray.NewProc("DestroyIcon")
	procCreatePopupMenu       = user32Tray.NewProc("CreatePopupMenu")
	procDestroyMenu           = user32Tray.NewProc("DestroyMenu")
	procAppendMenuW           = user32Tray.NewProc("AppendMenuW")
	procSetMenuDefaultItem    = user32Tray.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu        = user32Tray.NewProc("TrackPopupMenu")
	procSetForegroundWindow   = user32Tray.NewProc("SetForegroundWindow")
	procGetCursorPos          = user32Tray.NewProc("GetCursorPos")
	procShellNotifyIconW      = shell32Tray.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW      = kernel32Tray.NewProc("GetModuleHandleW")
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	x, y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

type TrayCallbacks struct {
	Tooltip         string
	IconPath        string
	OnOpenDashboard func()
	OnSendFiles     func()
	OnPushClipboard func()
	OnOpenFolder    func()
	IsAutostart     func() bool
	OnToggleAuto    func()
	IsSendTo        func() bool
	OnToggleSendTo  func()
	OnQuit          func()
}

type Tray struct {
	mu    sync.Mutex
	hwnd  uintptr
	hIcon uintptr
	cb    TrayCallbacks
}

var activeTray *Tray

// RunTray starts the Windows system tray message loop on the current goroutine (blocks until Quit).
func RunTray(cb TrayCallbacks) *Tray {
	t := &Tray{cb: cb}
	activeTray = t
	go t.loop()
	return t
}

func (t *Tray) Quit() {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmDestroy, 0, 0)
	}
}

func (t *Tray) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("AirDropWinTrayClass")

	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hInst,
		lpszClassName: className,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return
	}

	t.mu.Lock()
	t.hwnd = hwnd
	t.mu.Unlock()

	var hIcon uintptr
	if t.cb.IconPath != "" {
		ip, _ := syscall.UTF16PtrFromString(t.cb.IconPath)
		hIcon, _, _ = procLoadImageW.Call(0, uintptr(unsafe.Pointer(ip)), imageIcon, 32, 32, lrLoadFromFile|lrDefaultSize)
	}
	t.hIcon = hIcon

	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTrayIcon
	nid.hIcon = hIcon
	tip := t.cb.Tooltip
	if tip == "" {
		tip = "隔空传 (AirDropWin) - 点击打开"
	}
	copyUTF16(nid.szTip[:], tip)

	_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if hIcon != 0 {
		_, _, _ = procDestroyIcon.Call(hIcon)
	}
}

func trayWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	t := activeTray
	switch uMsg {
	case wmTrayIcon:
		ev := uint32(lParam & 0xffff)
		switch ev {
		case wmLButtonUp, wmLButtonDbl:
			if t != nil && t.cb.OnOpenDashboard != nil {
				go t.cb.OnOpenDashboard()
			}
		case wmRButtonUp, wmContextMenu:
			if t != nil {
				t.showMenu(hwnd)
			}
		}
		return 0
	case wmCommand:
		id := uint32(wParam & 0xffff)
		if t != nil {
			t.handleCommand(id)
		}
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return r
}

func (t *Tray) showMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendMenuItem(menu, mfString, cmdOpenDashboard, "打开 隔空传 (AirDropWin)")
	_, _, _ = procSetMenuDefaultItem.Call(menu, cmdOpenDashboard, 0)
	appendMenuItem(menu, mfSeparator, 0, "")
	appendMenuItem(menu, mfString, cmdSendFiles, "选择文件发送到手机 / Mac...")
	appendMenuItem(menu, mfString, cmdPushClipboard, "推送电脑剪贴板到手机 / Mac")
	appendMenuItem(menu, mfString, cmdOpenFolder, "打开接收文件夹")
	appendMenuItem(menu, mfSeparator, 0, "")

	autoFlags := uintptr(mfString)
	if t.cb.IsAutostart != nil && t.cb.IsAutostart() {
		autoFlags |= mfChecked
	}
	appendMenuItem(menu, autoFlags, cmdToggleAuto, "开机自动启动")

	sendToFlags := uintptr(mfString)
	if t.cb.IsSendTo != nil && t.cb.IsSendTo() {
		sendToFlags |= mfChecked
	}
	appendMenuItem(menu, sendToFlags, cmdToggleSendTo, "右键「发送到」菜单集成")

	appendMenuItem(menu, mfSeparator, 0, "")
	appendMenuItem(menu, mfString, cmdQuit, "退出 隔空传")

	var pt point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	_, _, _ = procTrackPopupMenu.Call(menu, tpmRightBtn|tpmBottomAlign, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
}

func (t *Tray) handleCommand(id uint32) {
	switch id {
	case cmdOpenDashboard:
		if t.cb.OnOpenDashboard != nil {
			go t.cb.OnOpenDashboard()
		}
	case cmdSendFiles:
		if t.cb.OnSendFiles != nil {
			go t.cb.OnSendFiles()
		}
	case cmdPushClipboard:
		if t.cb.OnPushClipboard != nil {
			go t.cb.OnPushClipboard()
		}
	case cmdOpenFolder:
		if t.cb.OnOpenFolder != nil {
			go t.cb.OnOpenFolder()
		}
	case cmdToggleAuto:
		if t.cb.OnToggleAuto != nil {
			go t.cb.OnToggleAuto()
		}
	case cmdToggleSendTo:
		if t.cb.OnToggleSendTo != nil {
			go t.cb.OnToggleSendTo()
		}
	case cmdQuit:
		if t.cb.OnQuit != nil {
			go t.cb.OnQuit()
		}
		t.Quit()
	}
}

func appendMenuItem(menu uintptr, flags uintptr, id uintptr, text string) {
	var ptr uintptr
	if text != "" {
		p, _ := syscall.UTF16PtrFromString(text)
		ptr = uintptr(unsafe.Pointer(p))
	}
	_, _, _ = procAppendMenuW.Call(menu, flags, id, ptr)
}

func copyUTF16(dst []uint16, s string) {
	u := utf16.Encode([]rune(s))
	if len(u) >= len(dst) {
		u = u[:len(dst)-1]
	}
	copy(dst, u)
	dst[len(u)] = 0
}
