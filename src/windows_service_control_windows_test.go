//go:build windows

package main

import (
	"testing"
	"unsafe"
)

func TestServiceProtocolRejectsUnexpectedCommands(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "automatic", "manual"} {
		got, e := serviceURIAction("spherelay://service/" + action)
		if e != nil || got != action {
			t.Fatalf("valid action rejected: %s", action)
		}
	}
	for _, uri := range []string{"https://service/start", "spherelay://service/start?cmd=anything", "spherelay://service/restart#x", "spherelay://user@service/start", "spherelay://service/../start", "spherelay://service/powershell", "spherelay://service/start/extra", "spherelay://service/%252fstart", "spherelay://tray/open"} {
		if _, e := serviceURIAction(uri); e == nil {
			t.Fatalf("unexpected protocol command accepted: %s", uri)
		}
	}
	if e := controlSpherelayService("unknown"); e == nil {
		t.Fatal("unknown action accepted")
	}
}
func TestTrayWindowsABI(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("x64 layout test")
	}
	if unsafe.Sizeof(trayIconData{}) != 976 || unsafe.Sizeof(trayWindowClass{}) != 80 || unsafe.Sizeof(trayMessage{}) != 48 {
		t.Fatal("Windows tray ABI mismatch")
	}
}
func TestServiceStatusReadOnly(t *testing.T) {
	info, e := readSpherelayService()
	if e != nil {
		t.Fatal(e)
	}
	if info.Installed && (info.State == "" || info.StartMode == "" || info.Account == "" || info.BinaryPath == "") {
		t.Fatal("incomplete Windows service status")
	}
}
