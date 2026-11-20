//go:build windows

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var advService = syscall.NewLazyDLL("advapi32.dll")
var closeServiceHandle = advService.NewProc("CloseServiceHandle")

func winText(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func serviceHandle(access uintptr) (uintptr, error) {
	manager, _, e := advService.NewProc("OpenSCManagerW").Call(0, 0, 1)
	if manager == 0 {
		return 0, e
	}
	defer closeServiceHandle.Call(manager)
	h, _, e := advService.NewProc("OpenServiceW").Call(manager, uintptr(unsafe.Pointer(winText("Spherelay"))), access)
	if h == 0 {
		return 0, e
	}
	return h, nil
}

type nativeServiceConfig struct {
	ServiceType, StartType, ErrorControl uint32
	BinaryPath, Group                    *uint16
	Tag                                  uint32
	Dependencies, Account, DisplayName   *uint16
}
type nativeServiceStatus struct{ Type, State, Accepted, ExitCode, Specific, Checkpoint, WaitHint, PID, Flags uint32 }

func ptrText(p *uint16) string {
	if p == nil {
		return ""
	}
	b := make([]uint16, 0, 128)
	for i := 0; i < 32768; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if v == 0 {
			return syscall.UTF16ToString(b)
		}
		b = append(b, v)
	}
	return ""
}
func queryServiceState(h uintptr) (uint32, error) {
	var status nativeServiceStatus
	var needed uint32
	ok, _, e := advService.NewProc("QueryServiceStatusEx").Call(h, 0, uintptr(unsafe.Pointer(&status)), unsafe.Sizeof(status), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return 0, e
	}
	return status.State, nil
}
func readSpherelayService() (*WindowsServiceInfo, error) {
	h, e := serviceHandle(5)
	if e == syscall.Errno(1060) {
		return &WindowsServiceInfo{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer closeServiceHandle.Call(h)
	buf := make([]byte, 8192)
	var needed uint32
	ok, _, e := advService.NewProc("QueryServiceConfigW").Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 {
		return nil, e
	}
	c := (*nativeServiceConfig)(unsafe.Pointer(&buf[0]))
	state, e := queryServiceState(h)
	if e != nil {
		return nil, e
	}
	var delayed uint32
	advService.NewProc("QueryServiceConfig2W").Call(h, 3, uintptr(unsafe.Pointer(&delayed)), 4, uintptr(unsafe.Pointer(&needed)))
	modes := map[uint32]string{2: "Automatisch", 3: "Manuell", 4: "Deaktiviert"}
	states := map[uint32]string{1: "Beendet", 2: "Wird gestartet", 3: "Wird beendet", 4: "Läuft", 7: "Angehalten"}
	return &WindowsServiceInfo{Installed: true, State: states[state], StartMode: modes[c.StartType], Account: ptrText(c.Account), BinaryPath: ptrText(c.BinaryPath), BeforeLogin: c.StartType == 2, Delayed: delayed != 0}, nil
}
func serviceURIAction(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "spherelay" || u.Host != "service" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Ungültiger Spherelay-Aufruf")
	}
	a := strings.TrimPrefix(u.Path, "/")
	if !validServiceAction(a) {
		return "", fmt.Errorf("Unbekannte Dienstaktion")
	}
	return a, nil
}
func requestServiceAction(action string) error {
	if !validServiceAction(action) {
		return fmt.Errorf("Unbekannte Dienstaktion")
	}
	exe := filepath.Join(os.Getenv("ProgramFiles"), "Spherelay", "Spherelay.exe")
	if _, e := os.Stat(exe); e != nil {
		return fmt.Errorf("Bitte Spherelay mit dem Installer einrichten")
	}
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(winText("runas"))), uintptr(unsafe.Pointer(winText(exe))), uintptr(unsafe.Pointer(winText("--service-admin "+action))), 0, 0)
	if r <= 32 {
		return fmt.Errorf("Windows-Dienstaktion wurde nicht gestartet oder abgebrochen")
	}
	return nil
}
func waitServiceState(h uintptr, want uint32) error {
	until := time.Now().Add(30 * time.Second)
	for time.Now().Before(until) {
		state, e := queryServiceState(h)
		if e != nil {
			return e
		}
		if state == want {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Windows-Dienst hat den gewünschten Zustand nicht rechtzeitig erreicht")
}
func controlSpherelayService(action string) error {
	if !validServiceAction(action) {
		return fmt.Errorf("Unbekannte Dienstaktion")
	}
	info, e := readSpherelayService()
	if e != nil {
		return e
	}
	if !info.Installed {
		return fmt.Errorf("Spherelay-Dienst ist nicht installiert")
	}
	expected := `"` + filepath.Join(os.Getenv("ProgramFiles"), "Spherelay", "Spherelay.exe") + `" --service`
	if !strings.HasPrefix(strings.ToLower(info.BinaryPath), strings.ToLower(expected)) {
		return fmt.Errorf("Dienst verweist nicht auf die installierte Spherelay-Programmdatei")
	}
	h, e := serviceHandle(0x37)
	if e != nil {
		return e
	}
	defer closeServiceHandle.Call(h)
	if action == "automatic" || action == "manual" {
		start := uintptr(3)
		if action == "automatic" {
			start = 2
		}
		ok, _, e := advService.NewProc("ChangeServiceConfigW").Call(h, 0xffffffff, start, 0xffffffff, 0, 0, 0, 0, 0, 0, 0)
		if ok == 0 {
			return e
		}
		var delay uint32
		ok, _, e = advService.NewProc("ChangeServiceConfig2W").Call(h, 3, uintptr(unsafe.Pointer(&delay)))
		if ok == 0 {
			return e
		}
		return nil
	}
	state, e := queryServiceState(h)
	if e != nil {
		return e
	}
	if action == "stop" || action == "restart" {
		if state != 1 {
			if state != 3 {
				var status nativeServiceStatus
				ok, _, e := advService.NewProc("ControlService").Call(h, 1, uintptr(unsafe.Pointer(&status)))
				if ok == 0 && e != syscall.Errno(1062) {
					return e
				}
			}
			if e = waitServiceState(h, 1); e != nil {
				return e
			}
		}
		if action == "stop" {
			return nil
		}
	}
	state, e = queryServiceState(h)
	if e != nil {
		return e
	}
	if state == 4 {
		return nil
	}
	if state == 3 {
		if e = waitServiceState(h, 1); e != nil {
			return e
		}
	}
	ok, _, e := advService.NewProc("StartServiceW").Call(h, 0, 0)
	if ok == 0 && e != syscall.Errno(1056) {
		return e
	}
	return waitServiceState(h, 4)
}
func showWindowsMessage(message string, failed bool) {
	flags := uintptr(0x40)
	if failed {
		flags = 0x10
	}
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(winText(message))), uintptr(unsafe.Pointer(winText("Spherelay"))), flags)
}
func handleWindowsControl(args []string) bool {
	if path := installerArg(args, "--installer-update-snapshot"); path != "" {
		if err := installerUpdateSnapshot(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	}
	if path := installerArg(args, "--installer-update-report"); path != "" {
		if err := installerUpdateReport(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	}
	if hasArgument(args, "--tray") {
		if e := runWindowsTray(false); e != nil {
			showWindowsMessage(e.Error(), true)
		}
		return true
	}
	if hasArgument(args, "--tray-self-test") {
		if e := runWindowsTray(true); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return true
	}
	if raw := installerArg(args, "--windows-action"); raw != "" {
		if strings.HasPrefix(raw, "spherelay://update/") {
			target, err := appUpdateProtocolVersion(raw)
			if err == nil {
				err = requestAppUpdate(target)
			}
			if err != nil {
				showWindowsMessage(err.Error(), true)
			}
			return true
		}
		if raw == "spherelay://tray/open" {
			openLocalUI()
			if e := runWindowsTray(false); e != nil {
				showWindowsMessage(e.Error(), true)
			}
			return true
		}
		a, e := serviceURIAction(raw)
		if e == nil {
			e = requestServiceAction(a)
		}
		if e != nil {
			showWindowsMessage(e.Error(), true)
		}
		return true
	}
	if target := installerArg(args, "--app-update-admin"); target != "" {
		if err := applyWindowsAppUpdate(target); err != nil {
			showWindowsMessage(err.Error(), true)
		} else {
			showWindowsMessage("Spherelay "+target+" ist installiert und der Dienst läuft wieder.", false)
		}
		return true
	}
	if a := installerArg(args, "--service-admin"); a != "" {
		e := controlSpherelayService(a)
		if e != nil {
			showWindowsMessage(e.Error(), true)
			os.Exit(1)
		}
		return true
	}
	return false
}

func startWindowsTray() {
	info, err := readSpherelayService()
	if err != nil || !info.Installed {
		return
	}
	exe := filepath.Join(os.Getenv("ProgramFiles"), "Spherelay", "Spherelay.exe")
	cmd := backgroundCommand(exe, "--tray")
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func openInstalledService() bool {
	info, err := readSpherelayService()
	if err != nil || !info.Installed {
		return false
	}
	if err = requestServiceAction("start"); err != nil {
		showWindowsMessage(err.Error(), true)
		return true
	}
	for i := 0; i < 120; i++ {
		if localSpherelayRunning() {
			startWindowsTray()
			openLocalUI()
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	showWindowsMessage("Der Dienst ist noch nicht erreichbar. Bitte den Dienststatus im Tray-Menü prüfen.", true)
	return true
}
