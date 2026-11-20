//go:build !windows

package main

func handleWindowsControl(args []string) bool { return false }
func startWindowsTray()                       {}
func openInstalledService() bool              { return false }
