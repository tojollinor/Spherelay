package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

// Opt-in diagnostic. The network and expected device are never built into the app.
func TestLiveAutomaticDiscovery(t *testing.T) {
	if os.Getenv("SPHERELAY_DISCOVERY_LIVE") != "1" {
		t.Skip("local-network diagnostic disabled")
	}
	s, err := newServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.sessions["diagnostic"] = Session{Token: "diagnostic", Expires: time.Now().Add(time.Minute)}
	started := time.Now()
	result := tRequest(t, s, "POST", "/api/discover", map[string]any{}, &http.Cookie{Name: "spherelay_session", Value: "diagnostic"})
	if result.Code != 200 {
		t.Fatalf("automatic scan: %d %s", result.Code, result.Body.String())
	}
	var report DiscoveryReport
	if err := json.Unmarshal(result.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("SPHERELAY_DISCOVERY_REPORT"); path != "" {
		if err := os.WriteFile(path, result.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	expected := os.Getenv("SPHERELAY_DIAGNOSTIC_IP")
	expectedModel := os.Getenv("SPHERELAY_DIAGNOSTIC_MODEL")
	found := expected == ""
	for _, device := range report.Devices {
		t.Logf("%s: %s; manufacturer=%s; model=%s — %s", device.Address, device.Category, device.Manufacturer, device.Model, device.Evidence)
		if device.Address == expected && (device.Category == "camera" || device.Category == "camera-hint") {
			found = true
			if expectedModel != "" && device.Model != expectedModel {
				t.Fatalf("device was found, but the expected model was not identified")
			}
		}
	}
	t.Logf("Automatic networks: %+v; HTTP targets %d/%d; elapsed %s; other web devices: %d; warnings: %v", report.Networks, report.Checked, report.Total, time.Since(started), len(report.Other), report.Warnings)
	if !found {
		t.Fatalf("diagnostic camera was not automatically identified")
	}
}
