//go:build !windows

package main

func canRunWindowsAppUpdate() bool { return false }
