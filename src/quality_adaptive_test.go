package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLensFitNotSceneBrightness(t *testing.T) {
	im := image.NewRGBA(image.Rect(0, 0, 640, 640))
	for y := 0; y < 640; y++ {
		for x := 0; x < 640; x++ {
			if math.Hypot(float64(x-337), float64(y-308)) < 258 {
				c := color.RGBA{180, 180, 180, 255}
				if x < 330 && y < 305 {
					c = color.RGBA{45, 45, 45, 255}
				}
				im.Set(x, y, c)
			}
			if x < 80 && y < 25 {
				im.Set(x, y, color.White)
			}
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, im, &jpeg.Options{Quality: 95})
	c := detectLensCircle(b.Bytes())
	if !c.Detected || math.Abs(c.X-337./640) > .008 || math.Abs(c.Y-308./640) > .008 || c.Radius > 258.*2/640 {
		t.Fatalf("fit failed %+v", c)
	}
	t.Logf("asymmetric scene fitted centre %+v", c)
	if data, e := os.ReadFile("../../alpha12-preview.jpg"); e == nil {
		t.Logf("current high-resolution frame fitted %+v", detectLensCircle(data))
	}
	if data, e := os.ReadFile("../../alpha6-original-frame.jpg"); e == nil {
		t.Logf("saved actual camera frame fitted %+v", detectLensCircle(data))
	}
}
func TestActualAdaptiveOSD(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	s, _ := newServer(t.TempDir())
	v := Stream{ID: "adaptive", Name: "Camera 01", Width: 640, Height: 480, FOV: 80, FPS: 4, OSD: &OSDConfig{ViewName: true, Color: "auto", TitlePosition: "top-left", SizePercent: 5}}
	options, e := s.preparePipelineOptions(Camera{Projection: "equirect"}, []Stream{v})
	if e != nil {
		t.Fatal(e)
	}
	for _, bg := range []string{"black", "white", "red"} {
		if bg == "red" {
			v.Width = 1920
			v.Height = 1080
		}
		args := ffmpegArgs("unused", "equirect", []Stream{v}, options)
		filter := ""
		for i, a := range args {
			if a == "-filter_complex" {
				filter = args[i+1]
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := backgroundCommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c="+bg+":s=960x960:rate=4", "-filter_complex", filter, "-map", "[v0]", "-frames:v", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
		var errText bytes.Buffer
		cmd.Stderr = &errText
		raw, e := cmd.Output()
		cancel()
		if e != nil {
			t.Fatalf("adaptive OSD %s: %v %s", bg, e, errText.String())
		}
		if len(raw) != v.Width*v.Height*3 {
			t.Fatal("wrong size")
		}
		contrasting := 0
		for y := 10; y < 55; y++ {
			for x := 10; x < 180; x++ {
				i := (y*v.Width + x) * 3
				rr, gg, bb := raw[i], raw[i+1], raw[i+2]
				if bg == "white" {
					if rr < 20 && gg < 20 && bb < 20 {
						contrasting++
					}
				} else if rr > 230 && gg > 230 && bb > 230 {
					contrasting++
				}
			}
		}
		if contrasting < 25 {
			t.Fatalf("no contrasting text on %s: %d", bg, contrasting)
		}
		t.Logf("%s background: %d contrasting text pixels", bg, contrasting)
	}
}
func TestActualSourceDimensions(t *testing.T) {
	if os.Getenv("SPHERELAY_PREVIEW_LIVE") != "1" {
		t.Skip("actual camera opt-in")
	}
	s, e := newServer(os.Getenv("SPHERELAY_DIAGNOSTIC_DATA"))
	if e != nil || len(s.store.Cameras) == 0 {
		t.Fatal("camera data unavailable")
	}
	source, e := unprotectSecret(s.store.Cameras[0].Secret)
	if e != nil {
		t.Fatal("camera source unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := backgroundCommandContext(ctx, s.ffmpegPath(), "-hide_banner", "-loglevel", "info", "-nostdin", "-rtsp_transport", "tcp", "-timeout", "8000000", "-i", source, "-frames:v", "1", "-an", "-f", "null", "-")
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal("source decoding failed; raw decoder message withheld")
	}
	re := regexp.MustCompile(`\b(\d{3,5})x(\d{3,5})\b`)
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "Video:") {
			m := re.FindStringSubmatch(line)
			if len(m) == 3 {
				w, _ := strconv.Atoi(m[1])
				h, _ := strconv.Atoi(m[2])
				t.Logf("Actual decoded camera source %dx%d", w, h)
				return
			}
		}
	}
	t.Fatal("source dimensions not parsed")
}

func TestUniformImageIsNotMisidentifiedAsLens(t *testing.T) {
	for _, c := range []color.Color{color.Black, color.White} {
		im := image.NewRGBA(image.Rect(0, 0, 320, 320))
		for y := 0; y < 320; y++ {
			for x := 0; x < 320; x++ {
				im.Set(x, y, c)
			}
		}
		var b bytes.Buffer
		jpeg.Encode(&b, im, nil)
		if fit := detectLensCircle(b.Bytes()); fit.Detected {
			t.Fatalf("uniform frame cropped %+v", fit)
		}
	}
}
