package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAlpha7DownloadProgressAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("archive-test")) }))
	defer server.Close()
	s := &Server{components: map[string]*ComponentStatus{"ffmpeg": {Status: "installing"}}}
	p, err := s.downloadComponentArchive(context.Background(), "ffmpeg", server.URL, 64)
	if err != nil || string(p) != "archive-test" || s.components["ffmpeg"].Received != 12 {
		t.Fatalf("progress: %v %+v", err, s.components["ffmpeg"])
	}
	if _, err = s.downloadComponentArchive(context.Background(), "ffmpeg", server.URL, 3); err == nil {
		t.Fatal("oversized archive accepted")
	}
}
func TestAlpha7ClockAdvances(t *testing.T) {
	s := &Server{dataDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.runOSDClock(ctx)
	path := filepath.Join(s.dataDir, "osd", "clock.txt")
	var a []byte
	var e error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, e = os.ReadFile(path)
		if e == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(1100 * time.Millisecond)
	b, e := os.ReadFile(path)
	if e != nil || string(a) == string(b) {
		t.Fatalf("clock did not advance %v", e)
	}
}
func TestAlpha7ActualReleaseChecks(t *testing.T) {
	if os.Getenv("SPHERELAY_RELEASE_LIVE") != "1" {
		t.Skip("official release network opt-in")
	}
	for _, name := range []string{"ffmpeg", "mediamtx"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		r, e := latestComponentRelease(ctx, name)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if len(r.SHA) != 64 || r.Version == "" {
			t.Fatal("unverifiable release")
		}
		t.Logf("%s official latest %s", name, r.Version)
	}
}
