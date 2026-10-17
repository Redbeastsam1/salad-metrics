//go:build windows

package tray

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	WM_DESTROY       = 0x0002
	WM_COMMAND       = 0x0111
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_USER          = 0x0400
	WM_TRAYCALLBACK  = WM_USER + 100

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	MF_STRING    = 0x00000000
	MF_SEPARATOR = 0x00000800

	TPM_BOTTOMALIGN = 0x0020
	TPM_RIGHTBUTTON = 0x0002

	SW_HIDE = 0
	SW_SHOW = 5

	IDI_APPLICATION = 32512

	IDM_OPEN           = 2001
	IDM_TOGGLE_CONSOLE = 2002
	IDM_QUIT           = 2003
)

type POINT struct {
	X int32
	Y int32
}

type MSG struct {
	HWnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             windows.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	TimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

var (
	appPort     int
	nid         NOTIFYICONDATAW
	trayHwnd    windows.HWND
	menuCreated uintptr
)

func wndProc(hwnd windows.HWND, msg uint32, wParam uintptr, lParam uintptr) uintptr {
	switch msg {
	case WM_TRAYCALLBACK:
		switch lParam {
		case WM_RBUTTONUP:
			var pt POINT
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

			hMenu, _, _ := procCreatePopupMenu.Call()
			openText, _ := windows.UTF16PtrFromString("🌐 Open Dashboard")
			consoleText, _ := windows.UTF16PtrFromString("👁️ Toggle Console Window")
			quitText, _ := windows.UTF16PtrFromString("❌ Quit Salad Metrics")

			procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(IDM_OPEN), uintptr(unsafe.Pointer(openText)))
			procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(IDM_TOGGLE_CONSOLE), uintptr(unsafe.Pointer(consoleText)))
			procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
			procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(IDM_QUIT), uintptr(unsafe.Pointer(quitText)))

			procSetForegroundWindow.Call(uintptr(hwnd))
			procTrackPopupMenu.Call(hMenu, uintptr(TPM_RIGHTBUTTON|TPM_BOTTOMALIGN), uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
			procDestroyMenu.Call(hMenu)
			return 0

		case WM_LBUTTONDBLCLK, WM_LBUTTONUP:
			OpenBrowser(appPort)
			return 0
		}

	case WM_COMMAND:
		cmdID := int(wParam & 0xffff)
		switch cmdID {
		case IDM_OPEN:
			OpenBrowser(appPort)
		case IDM_TOGGLE_CONSOLE:
			toggleConsole()
		case IDM_QUIT:
			Remove()
			os.Exit(0)
		}
		return 0

	case WM_DESTROY:
		Remove()
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

// OpenBrowser opens the dashboard in the user's default web browser.
func OpenBrowser(port int) {
	url := fmt.Sprintf("http://localhost:%d", port)
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func toggleConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible != 0 {
		procShowWindow.Call(hwnd, uintptr(SW_HIDE))
	} else {
		procShowWindow.Call(hwnd, uintptr(SW_SHOW))
	}
}

// Start registers the system tray icon and runs the Win32 message pump in a dedicated OS thread.
func Start(port int, machineName string, hideConsole bool) {
	appPort = port

	go func() {
		// Lock goroutine to an OS thread for Win32 message queue handling
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		className, _ := windows.UTF16PtrFromString("SaladMetricsTrayClass")
		hInst, _, _ := procGetModuleHandleW.Call(0)
		hInstance := windows.Handle(hInst)
		hIcon, _, _ := procLoadIconW.Call(0, uintptr(IDI_APPLICATION))

		wndClass := WNDCLASSEXW{
			CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
			LpfnWndProc:   windows.NewCallback(wndProc),
			HInstance:     hInstance,
			HIcon:         windows.Handle(hIcon),
			LpszClassName: className,
		}

		atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClass)))
		if atom == 0 {
			log.Printf("[tray] Failed to register window class")
			return
		}

		windowTitle, _ := windows.UTF16PtrFromString("SaladMetricsTrayWindow")
		h, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(windowTitle)),
			0,
			0, 0, 0, 0,
			0, 0,
			uintptr(hInstance),
			0,
		)
		trayHwnd = windows.HWND(h)
		if trayHwnd == 0 {
			log.Printf("[tray] Failed to create tray message window")
			return
		}

		tipText := fmt.Sprintf("Salad Metrics (%s) :%d", machineName, port)
		var tipChars [128]uint16
		utf16Tip, _ := windows.UTF16FromString(tipText)
		copy(tipChars[:], utf16Tip)

		nid = NOTIFYICONDATAW{
			CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
			HWnd:             trayHwnd,
			UID:              1,
			UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
			UCallbackMessage: WM_TRAYCALLBACK,
			HIcon:            windows.Handle(hIcon),
			SzTip:            tipChars,
		}

		ret, _, _ := procShellNotifyIconW.Call(uintptr(NIM_ADD), uintptr(unsafe.Pointer(&nid)))
		if ret == 0 {
			log.Printf("[tray] Shell_NotifyIconW failed")
			return
		}

		log.Printf("[tray] Windows system tray icon active. Double-click to open dashboard.")

		if hideConsole {
			consoleHwnd, _, _ := procGetConsoleWindow.Call()
			if consoleHwnd != 0 {
				procShowWindow.Call(consoleHwnd, uintptr(SW_HIDE))
			}
		}

		// Windows Message Loop
		var msg MSG
		for {
			res, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if res == 0 || int32(res) == -1 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
}

// Remove deletes the tray icon from the notification area.
func Remove() {
	if nid.HWnd != 0 {
		procShellNotifyIconW.Call(uintptr(NIM_DELETE), uintptr(unsafe.Pointer(&nid)))
		nid.HWnd = 0
	}
}
