//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var windowsIntegrationMu sync.Mutex

const windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func windowsCommand(exe, data string, open bool) string {
	cmd := syscall.EscapeArg(exe) + " --data " + syscall.EscapeArg(data)
	if open {
		cmd += " --open-ui"
	}
	return cmd
}
func windowsAssets() (string, string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		return "", "", fmt.Errorf("Windows-Benutzerprofil nicht verfügbar")
	}
	dir := filepath.Join(root, "Spherelay", "App", version)
	return physicalWindowsPath(filepath.Join(dir, "Spherelay.exe")), physicalWindowsPath(filepath.Join(dir, "Spherelay.ico")), nil
}
func runWindowsScript(script string) ([]byte, error) {
	units := utf16.Encode([]rune("$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false);" + script))
	raw := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(raw[i*2:], v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	out, err := backgroundCommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)).Output()
	if err != nil {
		return nil, fmt.Errorf("Windows-Einstellung konnte nicht gelesen oder gespeichert werden: %v", err)
	}
	return bytes.TrimPrefix(bytes.TrimSpace(out), []byte{0xef, 0xbb, 0xbf}), nil
}
func serviceLaunchMode() bool { return hasArgument(os.Args, "--service") }
func getWindowsIntegration(data string) (WindowsIntegrationStatus, error) {
	windowsIntegrationMu.Lock()
	defer windowsIntegrationMu.Unlock()
	return readWindowsIntegration(data)
}
func readWindowsIntegration(data string) (WindowsIntegrationStatus, error) {
	s := WindowsIntegrationStatus{Available: true, ServiceMode: serviceLaunchMode(), Version: version}
	s.Service, _ = readSpherelayService()
	if s.ServiceMode {
		s.Available = false
		s.Message = "Der Windows-Dienst startet bei Startart Automatisch bereits vor der Anmeldung. Das Tray-Symbol erscheint erst nach der Anmeldung und wird zur Steuerung verwendet."
		return s, nil
	}
	exe, _, err := windowsAssets()
	if err != nil {
		return s, err
	}
	script := "$key=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey(" + psLiteral(windowsRunKey) + ");$value=$null;if($key){$value=$key.GetValue('Spherelay');$key.Dispose()};$desktop=Join-Path ([Environment]::GetFolderPath('Desktop')) 'Spherelay.lnk';@{enabled=[bool]$value;current=($value -eq " + psLiteral(windowsCommand(exe, data, false)) + ");desktop=(Test-Path -LiteralPath $desktop);desktopPath=$desktop}|ConvertTo-Json -Compress"
	out, err := runWindowsScript(script)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(out, &s); err != nil {
		return s, fmt.Errorf("Windows-Status konnte nicht gelesen werden")
	}
	s.Available = true
	s.Version = version
	return s, nil
}
func installWindowsAssets() (string, string, error) {
	exe, icon, err := windowsAssets()
	if err != nil {
		return "", "", err
	}
	source, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		return "", "", err
	}
	if err = os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
		return "", "", err
	}
	current, _ := os.ReadFile(exe)
	if sha256.Sum256(current) != sha256.Sum256(payload) {
		tmp := exe + ".new"
		if err = os.WriteFile(tmp, payload, 0700); err != nil {
			return "", "", err
		}
		if err = replaceFile(tmp, exe); err != nil {
			os.Remove(tmp)
			return "", "", fmt.Errorf("Autostart-Programm konnte nicht aktualisiert werden: %w", err)
		}
	}
	ico, err := webAssets.ReadFile("web/logo.ico")
	if err != nil {
		return "", "", err
	}
	if err = os.WriteFile(icon, ico, 0600); err != nil {
		return "", "", err
	}
	return physicalWindowsPath(exe), physicalWindowsPath(icon), nil
}
func changeWindowsIntegration(data, action string, enabled bool) (WindowsIntegrationStatus, error) {
	windowsIntegrationMu.Lock()
	defer windowsIntegrationMu.Unlock()
	if serviceLaunchMode() {
		return WindowsIntegrationStatus{}, fmt.Errorf("Diese Einstellung ist nur im angemeldeten Windows-Benutzerkonto möglich")
	}
	if action == "autostart" {
		var command string
		if enabled {
			exe, _, err := installWindowsAssets()
			if err != nil {
				return WindowsIntegrationStatus{}, err
			}
			command = windowsCommand(exe, data, false)
		}
		script := windowsRegistryValueScript(windowsRunKey, "Spherelay", command, enabled)
		if _, err := runWindowsScript(script); err != nil {
			return WindowsIntegrationStatus{}, err
		}
	} else if action == "desktop" {
		exe, icon, err := installWindowsAssets()
		if err != nil {
			return WindowsIntegrationStatus{}, err
		}
		args := "--data " + syscall.EscapeArg(data) + " --open-ui"
		script := "$desktop=[Environment]::GetFolderPath('Desktop');if(!$desktop){throw 'Desktop unavailable'};[IO.Directory]::CreateDirectory($desktop)|Out-Null;$shell=New-Object -ComObject WScript.Shell;$shortcut=$shell.CreateShortcut((Join-Path $desktop 'Spherelay.lnk'));$shortcut.TargetPath=" + psLiteral(exe) + ";$shortcut.Arguments=" + psLiteral(args) + ";$shortcut.WorkingDirectory=" + psLiteral(data) + ";$shortcut.IconLocation=" + psLiteral(icon+",0") + ";$shortcut.Description='Spherelay öffnen';$shortcut.WindowStyle=7;$shortcut.Save()"
		if _, err = runWindowsScript(script); err != nil {
			return WindowsIntegrationStatus{}, err
		}
	} else {
		return WindowsIntegrationStatus{}, fmt.Errorf("Unbekannte Windows-Aktion")
	}
	s, err := readWindowsIntegration(data)
	if err != nil {
		return s, err
	}
	if action == "autostart" && (s.Enabled != enabled || (enabled && !s.Current)) {
		return s, fmt.Errorf("Autostart konnte nicht bestätigt werden")
	}
	if action == "desktop" && s.Desktop {
		notifyDesktopItem(s.DesktopPath)
	}
	if action == "desktop" && !s.Desktop {
		return s, fmt.Errorf("Desktop-Verknüpfung konnte nicht bestätigt werden")
	}
	return s, nil
}
func openLocalUI() {
	cmd := backgroundCommand(filepath.Join(os.Getenv("WINDIR"), "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", "http://127.0.0.1:8787/")
	if cmd.Start() == nil {
		go cmd.Wait()
	}
}

func windowsRegistryValueScript(key, name, command string, enabled bool) string {
	script := "$key=[Microsoft.Win32.Registry]::CurrentUser.CreateSubKey(" + psLiteral(key) + ");try{"
	if enabled {
		script += "$key.SetValue(" + psLiteral(name) + "," + psLiteral(command) + ",[Microsoft.Win32.RegistryValueKind]::String)"
	} else {
		script += "$key.DeleteValue(" + psLiteral(name) + ",$false)"
	}
	return script + "}finally{$key.Dispose()}"
}

var finalWindowsPathProc = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

func physicalWindowsPath(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return path
	}
	defer f.Close()
	buf := make([]uint16, 4096)
	n, _, _ := finalWindowsPathProc.Call(f.Fd(), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
	if n == 0 || n >= uintptr(len(buf)) {
		return path
	}
	actual := syscall.UTF16ToString(buf[:n])
	if strings.HasPrefix(actual, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(actual, `\\?\UNC\`)
	}
	return strings.TrimPrefix(actual, `\\?\`)
}
func notifyDesktopItem(path string) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	syscall.NewLazyDLL("shell32.dll").NewProc("SHChangeNotify").Call(0x2000, 0x1005, uintptr(unsafe.Pointer(p)), 0)
}
