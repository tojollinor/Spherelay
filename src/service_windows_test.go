//go:build windows

package main

import (
	"syscall"
	"testing"
)

func TestWindowsServiceCallbacks(t *testing.T) {
	// Regression: the original serviceMain panicked here before any HTTP listener existed.
	if syscall.NewCallback(serviceMain) == 0 || syscall.NewCallback(controlHandler) == 0 {
		t.Fatal("Windows callback creation failed")
	}
}
