package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOSDDefaultsFormatsAndValidation(t *testing.T) {
	o := effectiveOSD(Stream{})
	if o.ClockPosition != "top-left" || o.TitlePosition != "bottom-right" || o.ClockFormat != "dmy-weekday" || o.Font != "mono" || o.Style != "outline" || !o.ViewName || !o.Clock || o.CameraName {
		t.Fatal("reference defaults wrong")
	}
	date := time.Date(2026, 10, 30, 14, 5, 6, 0, time.UTC)
	for format, want := range map[string]string{"time": "14:05:06", "dmy": "30-10-2026 14:05:06", "dmy-weekday": "30-10-2026 Fri 14:05:06", "iso": "2026-10-30 14:05:06"} {
		if got := formatOSDTime(date, format); got != want {
			t.Fatalf("format %s: %s", format, got)
		}
	}
	for _, o := range []OSDConfig{{Color: "white:reload=1"}, {ClockFormat: "../secret"}, {TitlePosition: "outside"}, {SizePercent: 12}, {Font: "file=/tmp/a"}, {Style: "bad"}} {
		if validOSD(Stream{OSD: &o}) {
			t.Fatalf("invalid option accepted %+v", o)
		}
	}
}
func TestActualOSDReferenceLayoutAndOptions(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	s, e := newServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	positions := []string{"top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"}
	views := []Stream{{ID: "reference", Name: "Camera 01", Width: 640, Height: 480, FOV: 80, FPS: 5}}
	for i, p := range positions {
		formats := []string{"time", "dmy", "dmy-weekday", "iso"}
		styles := []string{"outline", "box", "plain"}
		views = append(views, Stream{ID: "test-" + p, Name: "Camera 01", Width: 640, Height: 480, FOV: 80, FPS: 5, OSD: &OSDConfig{Clock: true, ViewName: true, TitlePosition: p, ClockPosition: p, ClockFormat: formats[i%4], Style: styles[i%3], Font: "sans", Color: "#000000", SizePercent: 3}})
	}
	options, e := s.preparePipelineOptions(Camera{Projection: "equirect"}, views)
	if e != nil {
		t.Fatal(e)
	}
	render := func(list []Stream, raw bool) []byte {
		args := ffmpegArgs("unused", "equirect", list, options)
		filter := ""
		for i, a := range args {
			if a == "-filter_complex" {
				filter = args[i+1]
			}
		}
		a := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=960x960:rate=5", "-filter_complex", filter}
		for i := range list {
			a = append(a, "-map", "[v"+string(rune('0'+i))+"]", "-frames:v", "1")
			if raw {
				a = append(a, "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
			} else {
				a = append(a, "-f", "null", "-")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, e := backgroundCommandContext(ctx, ff, a...).Output()
		if e != nil {
			t.Fatalf("actual OSD filters: %v", e)
		}
		return out
	}
	raw := render(views[:1], true)
	if len(raw) != 640*480*3 {
		t.Fatal("wrong rendered dimensions")
	}
	counts := [4]int{}
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			i := (y*640 + x) * 3
			if raw[i] > 180 && raw[i+1] > 180 && raw[i+2] > 180 {
				q := 0
				if x >= 320 {
					q++
				}
				if y >= 240 {
					q += 2
				}
				counts[q]++
			}
		}
	}
	if counts[0] < 20 || counts[3] < 20 || counts[1] != 0 || counts[2] != 0 {
		t.Fatalf("wrong OSD corners %v", counts)
	}
	render(views[1:], false)
	for _, path := range options.ClockFiles {
		data, e := os.ReadFile(path)
		if e != nil || len(strings.TrimSpace(string(data))) < 8 {
			t.Fatal("clock text missing")
		}
	}
	t.Logf("actual default OSD corner pixels %v; six positions, all formats and three styles encoded", counts)
}
