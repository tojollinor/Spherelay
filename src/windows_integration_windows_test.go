//go:build windows

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWindowsIntegrationRegistryRoundTrip(t *testing.T) {
	key := `Software\Spherelay\IntegrationTest-` + token(10)
	defer runWindowsScript("[Microsoft.Win32.Registry]::CurrentUser.DeleteSubKeyTree(" + psLiteral(key) + ",$false)")
	command := windowsCommand(`C:\Programme\Spherelay\Spherelay.exe`, `C:\Eigene Daten\Kamera's Büro`, false)
	if !strings.Contains(command, `"C:\Eigene Daten\Kamera's Büro"`) {
		t.Fatal("data path not quoted")
	}
	if _, err := runWindowsScript(windowsRegistryValueScript(key, "TestValue", command, true)); err != nil {
		t.Fatal(err)
	}
	read := "$k=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey(" + psLiteral(key) + ");@{value=$k.GetValue('TestValue')}|ConvertTo-Json -Compress;$k.Dispose()"
	out, err := runWindowsScript(read)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(out, &got) != nil || got.Value != command {
		t.Fatalf("registry output: %q", out)
	}
	if _, err = runWindowsScript(windowsRegistryValueScript(key, "TestValue", "", false)); err != nil {
		t.Fatal(err)
	}
	out, err = runWindowsScript(read)
	if err != nil {
		t.Fatal(err)
	}
	got.Value = ""
	json.Unmarshal(out, &got)
	if got.Value != "" {
		t.Fatal("registry setting not removed")
	}
	t.Log("Windows registry write/read/remove passed in isolated test key; Run key untouched")
}
func TestWindowsIntegrationRequiresAuthentication(t *testing.T) {
	s, _ := newServer(t.TempDir())
	if w := tRequest(t, s, "GET", "/api/windows", nil, nil); w.Code != 401 {
		t.Fatal("unauthenticated Windows status accessible")
	}
	if w := tRequest(t, s, "POST", "/api/windows", map[string]any{"action": "desktop"}, nil); w.Code != 401 {
		t.Fatal("unauthenticated Windows changes accepted")
	}
}
func TestWindowsIntegrationStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Windows status read")
	}
	s, err := getWindowsIntegration(t.TempDir())
	if err != nil || !s.Available {
		t.Fatalf("Windows status read: %v", err)
	}
	t.Logf("Windows status read succeeded; autostart enabled=%v", s.Enabled)
}
