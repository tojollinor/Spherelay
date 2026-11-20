package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBaselineTestUsesSeparateRTSPPath(t *testing.T) {
	args := baselineTestArgs("view-example", 25)
	command := strings.Join(args, " ")
	for _, want := range []string{"-i rtsp://127.0.0.1:8554/view-example ", "-profile:v baseline", "-g 50", "rtsp://127.0.0.1:8554/view-example-baseline"} {
		if !strings.Contains(command, want) {
			t.Fatalf("missing %q in %q", want, command)
		}
	}
	if args[len(args)-1] != "rtsp://127.0.0.1:8554/view-example-baseline" {
		t.Fatal("test encoder must publish to its own path")
	}
}

func TestBaselineTestRequiresRunningOriginal(t *testing.T) {
	s := &Server{}
	s.store.Streams = []Stream{{ID: "view-example", CameraID: "cam-example", Enabled: true}}
	request := httptest.NewRequest("POST", "/api/streams/baseline", strings.NewReader(`{"id":"view-example","start":true}`))
	response := httptest.NewRecorder()
	s.baselineTestAPI(response, request)
	if response.Code != 409 {
		t.Fatalf("expected a conflict for a stopped original, got %d: %s", response.Code, response.Body.String())
	}
}
