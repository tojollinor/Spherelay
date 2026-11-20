//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackgroundCommandNoConsoleAndOutput(t *testing.T) {
	exe := filepath.Join(os.Getenv("WINDIR"), "System32", "cmd.exe")
	cmd := backgroundCommand(exe, "/d", "/c", "echo", "Spherelay-background-test")
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("no-console flags missing")
	}
	output, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(output)) != "Spherelay-background-test" {
		t.Fatalf("background output failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd = backgroundCommandContext(ctx, exe, "/d", "/c", "exit", "7")
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("context no-console flags missing")
	}
	if err = cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatal("exit status lost")
	}
}
