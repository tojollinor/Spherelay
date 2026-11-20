package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLiveSavedCredentialCameraDetails(t *testing.T) {
	if os.Getenv("SPHERELAY_DETAILS_LIVE") != "1" {
		t.Skip("saved-credential camera diagnostic disabled")
	}
	s, err := newServer(os.Getenv("SPHERELAY_DIAGNOSTIC_DATA"))
	if err != nil {
		t.Fatal("Diagnostic data directory unavailable")
	}
	creds, err := s.readDiscoveryCredentials()
	if err != nil || creds == nil {
		t.Fatal("Saved camera credential unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	details, err := inspectCamera(ctx, os.Getenv("SPHERELAY_DIAGNOSTIC_BASE"), creds, "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(details, "", "  ")
	if path := os.Getenv("SPHERELAY_DIAGNOSTIC_REPORT"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Model %s; firmware %s; sources %d", details.Model, details.Firmware, len(details.Sources))
	for _, source := range details.Sources {
		t.Logf("Channel %s: %s %dx%d %.2f fps; RTSP verified=%v", source.ID, source.Codec, source.Width, source.Height, source.FPS, source.Verified)
	}
}

func TestLiveOneRTSPDescription(t *testing.T) {
	if os.Getenv("SPHERELAY_RTSP_LIVE") != "1" {
		t.Skip("single RTSP diagnostic disabled")
	}
	s, err := newServer(os.Getenv("SPHERELAY_DIAGNOSTIC_DATA"))
	if err != nil {
		t.Fatal("Diagnostic data directory unavailable")
	}
	creds, err := s.readDiscoveryCredentials()
	if err != nil || creds == nil {
		t.Fatal("Saved credential unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	verified, status := verifyCameraRTSP(ctx, os.Getenv("SPHERELAY_DIAGNOSTIC_RTSP"), creds)
	t.Logf("RTSP verified=%v; %s", verified, status)
}
