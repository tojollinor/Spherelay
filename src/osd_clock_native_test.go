package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeClockDoesNotReloadFiles(t *testing.T) {
	for _, format := range []string{"time", "dmy", "dmy-weekday", "iso"} {
		filter := osdFilters(Stream{OSD: &OSDConfig{Clock: true, ClockFormat: format}}, pipelineOptions{Clock: "unreadable-clock.txt"})
		if strings.Contains(filter, "reload=") || strings.Contains(filter, "textfile=") || !strings.Contains(filter, "localtime") {
			t.Fatal("clock still depends on file reads")
		}
	}
}
func TestActualNativeClockSurvivesMissingFile(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	options := pipelineOptions{Clock: filepath.Join(t.TempDir(), "nonexistent-clock.txt"), Font: filepath.Join(os.Getenv("WINDIR"), "Fonts", "consola.ttf")}
	for _, format := range []string{"time", "dmy", "dmy-weekday", "iso"} {
		filter := strings.TrimPrefix(osdFilters(Stream{OSD: &OSDConfig{Clock: true, ClockFormat: format}}, options), ",")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, e := exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=320x120:r=25", "-vf", filter, "-frames:v", "75", "-f", "null", "-").CombinedOutput()
		cancel()
		if e != nil {
			t.Fatalf("clock %s failed without clock file: %v %s", format, e, out)
		}
	}
}
