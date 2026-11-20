//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var trayUser = syscall.NewLazyDLL("user32.dll")
var trayShell = syscall.NewLazyDLL("shell32.dll")

const trayCallback = 0x8001

type trayWindowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type trayPoint struct{ X, Y int32 }
type trayMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          trayPoint
	Private        uint32
}
type trayIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	Title               [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	Balloon             uintptr
}

func trayMenu(window uintptr) {
	menu, _, _ := trayUser.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		return
	}
	defer trayUser.NewProc("DestroyMenu").Call(menu)
	add := func(id uintptr, text string) {
		trayUser.NewProc("AppendMenuW").Call(menu, 0, id, uintptr(unsafe.Pointer(winText(text))))
	}
	add(1, "Öffnen")
	add(2, "Starten")
	add(3, "Neustarten")
	add(4, "Beenden (Dienst und Streams)")
	var point trayPoint
	trayUser.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	trayUser.NewProc("SetForegroundWindow").Call(window)
	chosen, _, _ := trayUser.NewProc("TrackPopupMenu").Call(menu, 0x102, uintptr(point.X), uintptr(point.Y), 0, window, 0)
	trayUser.NewProc("PostMessageW").Call(window, 0, 0, 0)
	switch chosen {
	case 1:
		openLocalUI()
	case 2, 3, 4:
		action := map[uintptr]string{2: "start", 3: "restart", 4: "stop"}[chosen]
		go func() {
			if e := requestServiceAction(action); e != nil {
				showWindowsMessage(e.Error(), true)
			}
		}()
	}
}
func runWindowsTray(selfTest bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mutexName := "Local\\Spherelay.Tray"
	if selfTest {
		mutexName += ".SelfTest"
	}
	mutex, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(winText(mutexName))))
	if mutex == 0 {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(mutex))
	if err == syscall.Errno(183) {
		return nil
	}
	exe, _ := os.Executable()
	iconPath := filepath.Join(filepath.Dir(exe), "Spherelay.ico")
	if _, err := os.Stat(iconPath); err != nil {
		_, p, e := installWindowsAssets()
		if e != nil {
			return e
		}
		iconPath = p
	}
	icon, _, e := trayUser.NewProc("LoadImageW").Call(0, uintptr(unsafe.Pointer(winText(iconPath))), 1, 32, 32, 0x10)
	if icon == 0 {
		return fmt.Errorf("Tray-Logo konnte nicht geladen werden: %v", e)
	}
	defer trayUser.NewProc("DestroyIcon").Call(icon)
	instance, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	taskbarCreated, _, _ := trayUser.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(winText("TaskbarCreated"))))
	var data trayIconData
	visible := false
	addIcon := func() bool {
		ok, _, _ := trayShell.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&data)))
		visible = ok != 0
		return ok != 0
	}
	refreshIcon := func() {
		info, err := readSpherelayService()
		if err != nil {
			return
		}
		show := info.Installed && info.State != "Beendet"
		if show && !visible {
			addIcon()
		}
		if !show && visible {
			trayShell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&data)))
			visible = false
		}
	}
	callback := syscall.NewCallback(func(window uintptr, msg uint32, w, l uintptr) uintptr {
		switch {
		case msg == trayCallback:
			if l == 0x203 {
				openLocalUI()
			} else if l == 0x205 || l == 0x7b {
				trayMenu(window)
			}
			return 0
		case msg == uint32(taskbarCreated):
			visible = false
			refreshIcon()
			return 0
		case msg == 0x113 && selfTest:
			trayUser.NewProc("DestroyWindow").Call(window)
			return 0
		case msg == 0x113:
			refreshIcon()
			return 0
		case msg == 0x10:
			// The icon belongs to the service; it cannot be closed independently.
			return 0
		case msg == 2:
			trayShell.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&data)))
			trayUser.NewProc("PostQuitMessage").Call(0)
			return 0
		}
		result, _, _ := trayUser.NewProc("DefWindowProcW").Call(window, uintptr(msg), w, l)
		return result
	})
	name := winText("SpherelayTrayWindow")
	class := trayWindowClass{Size: uint32(unsafe.Sizeof(trayWindowClass{})), Proc: callback, Instance: instance, Name: name, Icon: icon, SmallIcon: icon}
	atom, _, e := trayUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return e
	}
	defer trayUser.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), instance)
	window, _, e := trayUser.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(winText("Spherelay"))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		return e
	}
	data = trayIconData{Size: uint32(unsafe.Sizeof(trayIconData{})), Window: window, ID: 1, Flags: 7, Callback: trayCallback, Icon: icon}
	copy(data.Tip[:], syscall.StringToUTF16("Spherelay – Windows-Dienst verwalten"))
	if selfTest && !addIcon() {
		trayUser.NewProc("DestroyWindow").Call(window)
		return fmt.Errorf("Windows-Infotray ist nicht verfügbar")
	}
	if selfTest {
		trayUser.NewProc("SetTimer").Call(window, 1, 500, 0)
	} else {
		refreshIcon()
		trayUser.NewProc("SetTimer").Call(window, 1, 1000, 0)
	}
	var message trayMessage
	for {
		ret, _, e := trayUser.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(ret) == -1 {
			return e
		}
		if ret == 0 {
			break
		}
		trayUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&message)))
		trayUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&message)))
	}
	return nil
}
