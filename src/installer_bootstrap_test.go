package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestInstallerBootstrapPreservesAndProvisions(t *testing.T) {
	s, _ := newServer(t.TempDir())
	if provisionInstaller(s, "short", true, true) == nil {
		t.Fatal("short password accepted")
	}
	secret, _ := protectSecret("rtsp://user:secret@192.168.1.1/test")
	s.store.Cameras = []Camera{{ID: "cam-test", Enabled: true, Secret: secret}}
	s.store.Streams = []Stream{{ID: "view-test", CameraID: "cam-test", Enabled: true}}
	if err := provisionInstaller(s, "test-only-long-password", true, true); err != nil {
		t.Fatal(err)
	}
	saved, _ := newServer(s.dataDir)
	hash := saved.store.PasswordHash
	if hash != hashPassword(saved.store.Salt, "test-only-long-password") || !saved.store.Cameras[0].AutoStart || !saved.store.NetworkRTSP {
		t.Fatal("bootstrap incomplete")
	}
	if err := provisionInstaller(saved, "", false, false); err != nil {
		t.Fatal(err)
	}
	if saved.store.PasswordHash != hash {
		t.Fatal("existing password overwritten")
	}
	if _, err := unprotectSecret(saved.store.Cameras[0].Secret); err != nil {
		t.Fatal("imported credentials lost")
	}
}
func TestInstallerPasswordUTF16(t *testing.T) {
	text := "Abcdefghij ü & ' 12"
	units := utf16.Encode([]rune(text))
	raw := []byte{255, 254}
	for _, u := range units {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], u)
		raw = append(raw, b[:]...)
	}
	if decodeInstallerPassword(raw) != text {
		t.Fatal("password encoding changed")
	}
}
func TestInstallerRejectsUnverifiedBinaryBeforeExecution(t *testing.T) {
	s, _ := newServer(t.TempDir())
	path := filepath.Join(s.binDir, "unknown.exe")
	os.WriteFile(path, []byte("untrusted"), 0600)
	if installerComponent(s, "unknown") == nil {
		t.Fatal("unknown component accepted")
	}
}
