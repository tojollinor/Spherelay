package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManualViewStopPersistsWithoutDisablingView(t *testing.T) {
	directory := t.TempDir()
	s, err := newServer(directory)
	if err != nil {
		t.Fatal(err)
	}
	s.store.Cameras = []Camera{{ID: "cam-one", AutoStart: true}}
	s.store.Streams = []Stream{{ID: "view-one", CameraID: "cam-one", Enabled: true}}
	reply := httptest.NewRecorder()
	s.streamControlAPI(reply, httptest.NewRequest("POST", "/api/streams/control", strings.NewReader(`{"id":"view-one","action":"stop"}`)))
	if reply.Code != 200 {
		t.Fatal(reply.Body.String())
	}
	reloaded, err := newServer(directory)
	if err != nil {
		t.Fatal(err)
	}
	view := reloaded.store.Streams[0]
	if !view.Enabled || !view.Paused || reloaded.hasActiveViewsLocked("cam-one") {
		t.Fatalf("unexpected stop state: %+v", view)
	}
}

func TestStreamStatusSeparatesOfflineAndUnhealthy(t *testing.T) {
	s := &Server{store: Store{Cameras: []Camera{{ID: "cam-one", AutoStart: true}},
		Streams: []Stream{{ID: "view-down", CameraID: "cam-one", Enabled: true}, {ID: "view-stopped", CameraID: "cam-one", Enabled: true, Paused: true}}}}
	s.recordCameraRetryLocked("cam-one", "Verbindung fehlgeschlagen")
	reply := httptest.NewRecorder()
	s.streamStatusAPI(reply, httptest.NewRequest("GET", "/api/streams/status", nil))
	var result struct{ Streams []streamStatus }
	if err := json.Unmarshal(reply.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Streams) != 2 || result.Streams[0].State != "unhealthy" || result.Streams[0].NextRetry == nil || result.Streams[1].State != "offline" {
		t.Fatal(reply.Body.String())
	}
}

func TestBackupExcludesPasswordsAndRTSPQuery(t *testing.T) {
	s := &Server{store: Store{PasswordHash: "app-login-hash", ViewerPassword: "viewer-secret",
		Cameras: []Camera{{ID: "cam-one", URL: "rtsp://user:password@192.168.1.21/live?token=secret", Secret: "protected-secret"}}}}
	reply := httptest.NewRecorder()
	s.configurationBackupAPI(reply, httptest.NewRequest("GET", "/api/config/backup", nil))
	text := reply.Body.String()
	for _, secret := range []string{"app-login-hash", "viewer-secret", "password@", "token=", "protected-secret"} {
		if strings.Contains(text, secret) {
			t.Fatal("backup contains a secret")
		}
	}
	if !strings.Contains(text, "rtsp://192.168.1.21/live") {
		t.Fatal(text)
	}
}

func TestRepeatedDiagnosticWarningIsSummarized(t *testing.T) {
	s := &Server{}
	event := diagnosticEvent{Component: "ffmpeg", Level: "warn", Camera: "cam-one", Message: "Kamera nicht erreichbar"}
	s.recordDiagnostic(event)
	first := s.diagnosticEvents[0].Time
	s.recordDiagnostic(event)
	if len(s.diagnosticEvents) != 1 || s.diagnosticEvents[0].Count != 2 || s.diagnosticEvents[0].LastTime == nil ||
		!s.diagnosticEvents[0].Time.Equal(first) || s.diagnosticEvents[0].LastTime.Before(first) {
		t.Fatalf("unexpected diagnostic events: %+v", s.diagnosticEvents)
	}
}
