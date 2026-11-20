package main

import (
	"context"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSelectedAspectUsesSquarePixels(t *testing.T) {
	for _, size := range [][2]int{{960, 720}, {1280, 720}, {1920, 1080}, {640, 480}, {720, 1280}} {
		for _, fov := range []float64{25, 80, 140} {
			v := Stream{Width: size[0], Height: size[1], FOV: fov}
			x, _, z := streamRay(1, 0, v)
			_, y, z2 := streamRay(0, 1, v)
			if math.Abs((x/z)/float64(v.Width)-(y/z2)/float64(v.Height)) > 1e-10 {
				t.Fatalf("unequal pixel scale %dx%d fov %.0f", size[0], size[1], fov)
			}
		}
	}
}
func TestMirrorsPersistThroughAPI(t *testing.T) {
	s, e := newServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.store.Cameras = []Camera{{ID: "cam-test", Name: "Camera 01", Projection: "fisheye"}}
	s.sessions["test-session"] = Session{Token: "test-session", Expires: time.Now().Add(time.Minute)}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test-session"}
	v := Stream{CameraID: "cam-test", Name: "Camera 01", Width: 1280, Height: 720, FOV: 80, FPS: 5, Enabled: true, MirrorHorizontal: true, MirrorVertical: true}
	w := tRequest(t, s, "POST", "/api/streams", v, cookie)
	if w.Code != 200 {
		t.Fatalf("save %d", w.Code)
	}
	saved, e := newServer(s.dataDir)
	if e != nil || len(saved.store.Streams) != 1 || !saved.store.Streams[0].MirrorHorizontal || !saved.store.Streams[0].MirrorVertical {
		t.Fatal("mirror settings not persisted")
	}
	w = tRequest(t, s, "GET", "/api/state", nil, cookie)
	if !strings.Contains(w.Body.String(), `"mirrorHorizontal":true`) || !strings.Contains(w.Body.String(), `"mirrorVertical":true`) {
		t.Fatal("mirror flags not returned")
	}
}
func TestActualAspectAndMirroredPixels(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	source := "color=c=red:s=960x960,drawbox=x=480:y=0:w=480:h=480:color=green:t=fill,drawbox=x=0:y=480:w=480:h=480:color=blue:t=fill,drawbox=x=480:y=480:w=480:h=480:color=yellow:t=fill"
	for _, size := range [][2]int{{640, 480}, {1280, 720}} {
		var baseline []byte
		for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
			v := Stream{ID: "test", Width: size[0], Height: size[1], FOV: 80, FPS: 5, MirrorHorizontal: flags[0], MirrorVertical: flags[1], OSD: &OSDConfig{}}
			args := ffmpegArgs("unused", "equirect", []Stream{v})
			filter := ""
			for i, a := range args {
				if a == "-filter_complex" {
					filter = args[i+1]
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			cmd := backgroundCommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", source, "-filter_complex", filter, "-map", "[v0]", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
			out, e := cmd.Output()
			cancel()
			if e != nil {
				t.Fatalf("FFmpeg failed %v", e)
			}
			if len(out) != v.Width*v.Height*3 {
				t.Fatalf("wrong dimensions: %d bytes", len(out))
			}
			if baseline == nil {
				baseline = out
				continue
			}
			for _, x := range []int{v.Width / 4, 3 * v.Width / 4} {
				for _, y := range []int{v.Height / 4, 3 * v.Height / 4} {
					bx, by := x, y
					if flags[0] {
						bx = v.Width - 1 - x
					}
					if flags[1] {
						by = v.Height - 1 - y
					}
					for c := 0; c < 3; c++ {
						diff := int(out[(y*v.Width+x)*3+c]) - int(baseline[(by*v.Width+bx)*3+c])
						if diff < -3 || diff > 3 {
							t.Fatalf("wrong mirror pixels %dx%d horizontal=%v vertical=%v", v.Width, v.Height, flags[0], flags[1])
						}
					}
				}
			}
		}
		t.Logf("Actual %dx%d: dimensions and all mirror combinations passed", size[0], size[1])
	}
}
