//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPhysicalAssetPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Symbol ü.ico")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	actual := physicalWindowsPath(path)
	if strings.HasPrefix(actual, `\\?\`) {
		t.Fatal("extended path prefix not normalized")
	}
	a, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.Stat(actual)
	if e != nil || !os.SameFile(a, b) {
		t.Fatal("resolved path does not identify same file")
	}
}
