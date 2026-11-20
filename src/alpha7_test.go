package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlpha7LensAndBoundary(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			if math.Hypot(float64(x-200), float64(y-200)) < 164 {
				im.Set(x, y, color.White)
			}
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, im, nil)
	c := detectLensCircle(b.Bytes())
	if !c.Detected || math.Abs(c.Radius-.81) > .025 {
		t.Fatalf("circle %+v", c)
	}
	for yaw := -180.; yaw <= 180; yaw += 15 {
		for pitch := -90.; pitch <= 90; pitch += 15 {
			v := constrainFisheye(Stream{Yaw: yaw, Pitch: pitch, Roll: 47, FOV: 100, Width: 960, Height: 720})
			for _, x := range []float64{-1, 1} {
				for _, y := range []float64{-1, 1} {
					_, _, z := streamRay(x, y, v)
					if z < .0119 {
						t.Fatalf("outside: %+v", v)
					}
				}
			}
		}
	}
}
func TestAlpha7Versions(t *testing.T) {
	if !newerVersion("1.21.3", "v1.9.9") || newerVersion("9.0.2", "9.0.2") || newerVersion("8.1.0", "9.0.0") {
		t.Fatal("version comparison")
	}
}
func TestAlpha7ActualOSD(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	dir := t.TempDir()
	title := filepath.Join(dir, "title.txt")
	clock := filepath.Join(dir, "clock.txt")
	os.WriteFile(title, []byte("Camera 01 / Ansicht 1"), 0600)
	writeOSDClock(clock)
	v := Stream{ID: "test", Width: 640, Height: 480, FOV: 80, FPS: 5, Roll: 35}
	options := pipelineOptions{Circle: defaultCircle(), Titles: map[string]string{"test": title}, Clock: clock, Font: filepath.Join(os.Getenv("WINDIR"), "Fonts", "arial.ttf")}
	args := ffmpegArgs("unused", "fisheye", []Stream{v}, options)
	var filter string
	for i, a := range args {
		if a == "-filter_complex" {
			filter = args[i+1]
		}
	}
	cmd := exec.CommandContext(context.Background(), ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=960x960:rate=5", "-filter_complex", filter, "-map", "[v0]", "-frames:v", "2", "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("OSD encoding: %v %s", err, out)
	}
	if !strings.Contains(filter, "roll=0.0000") {
		t.Fatal("radial centre orientation missing")
	}
	if raw, err := os.ReadFile(filepath.Join("..", "..", "alpha6-original-frame.jpg")); err == nil {
		t.Logf("Actual saved frame circle: %+v", detectLensCircle(raw))
	}
}
