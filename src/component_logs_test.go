package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentLogWriterRedactsSplitCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := &componentLogWriter{file: &output}
	for _, chunk := range []string{"opening rtsp://admin:", "my-secret@192.168.1.10/live?token=hidden\n", "Authorization: Digest credential"} {
		if _, err := logger.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := logger.flush(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, secret := range []string{"admin", "my-secret", "hidden", "credential"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q remained in the log", secret)
		}
	}
	if !strings.Contains(got, "192.168.1.10/live") || !strings.Contains(got, "Authorization: [geschützt]") {
		t.Fatalf("diagnostic information missing: %q", got)
	}
}

func TestComponentLogsFilterCameraAndReportActiveLevel(t *testing.T) {
	server := &Server{
		dataDir: t.TempDir(),
		store: Store{
			Cameras:   []Camera{{ID: "camera-1"}},
			LogLevels: componentLogLevels{MediaMTX: "debug", FFmpeg: "info"},
		},
		processes: map[string]*Process{"camera-1": {LogLevel: "warn"}},
	}
	if err := os.WriteFile(filepath.Join(server.dataDir, "ffmpeg-camera-1.log"), []byte("rtsp://user:password@127.0.0.1/live\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result := httptest.NewRecorder()
	server.componentLogsAPI(result, httptest.NewRequest("GET", "/api/logs/components?component=ffmpeg&cameraId=camera-1", nil))
	got := result.Body.String()
	if result.Code != 200 || !strings.Contains(got, "127.0.0.1/live") || strings.Contains(got, "password") ||
		!strings.Contains(got, `"camera-1":"warn"`) || !strings.Contains(got, `"ffmpeg":"info"`) {
		t.Fatalf("unexpected diagnostics result: %s", got)
	}
	result = httptest.NewRecorder()
	server.componentLogsAPI(result, httptest.NewRequest("GET", "/api/logs/components?component=ffmpeg&cameraId=other", nil))
	if !strings.Contains(result.Body.String(), `"content":""`) {
		t.Fatal("unexpected content for an unknown camera")
	}
}
