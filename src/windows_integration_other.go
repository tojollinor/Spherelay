//go:build !windows

package main

import "fmt"

func getWindowsIntegration(data string) (WindowsIntegrationStatus, error) {
	return WindowsIntegrationStatus{Message: "Nur unter Windows verfügbar"}, nil
}
func changeWindowsIntegration(data, action string, enabled bool) (WindowsIntegrationStatus, error) {
	return WindowsIntegrationStatus{}, fmt.Errorf("Nur unter Windows verfügbar")
}
func openLocalUI() {}
