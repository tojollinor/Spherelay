package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type WindowsIntegrationStatus struct {
	Service     *WindowsServiceInfo `json:"service,omitempty"`
	Available   bool                `json:"available"`
	Enabled     bool                `json:"enabled"`
	Current     bool                `json:"current"`
	Desktop     bool                `json:"desktop"`
	DesktopPath string              `json:"desktopPath,omitempty"`
	ServiceMode bool                `json:"serviceMode"`
	Version     string              `json:"version"`
	Message     string              `json:"message,omitempty"`
}

func (s *Server) windowsIntegrationAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		status, err := getWindowsIntegration(s.dataDir)
		if err != nil {
			bad(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, status)
		return
	}
	var in struct {
		Action  string `json:"action"`
		Enabled bool   `json:"enabled"`
	}
	if !readReq(w, r, &in) {
		return
	}
	if in.Action != "autostart" && in.Action != "desktop" {
		bad(w, 400, "Unbekannte Windows-Aktion")
		return
	}
	status, err := changeWindowsIntegration(s.dataDir, in.Action, in.Enabled)
	if err != nil {
		bad(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, status)
}
func localSpherelayRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8787/api/info", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var info struct {
		Version string `json:"version"`
	}
	return res.StatusCode == 200 && json.NewDecoder(res.Body).Decode(&info) == nil && strings.HasPrefix(info.Version, "0.1.")
}
func hasArgument(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
func handleWindowsLaunch(args []string, data string) bool {
	if hasArgument(args, "--create-desktop-shortcut") {
		status, err := changeWindowsIntegration(data, "desktop", false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		json.NewEncoder(os.Stdout).Encode(status)
		return true
	}
	if !hasArgument(args, "--open-ui") {
		return false
	}
	if localSpherelayRunning() {
		startWindowsTray()
		openLocalUI()
		return true
	}
	if openInstalledService() {
		return true
	}
	go func() {
		for i := 0; i < 100; i++ {
			if localSpherelayRunning() {
				openLocalUI()
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
	}()
	return false
}
