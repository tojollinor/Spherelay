package main

import (
	"context"
	"math"
	"os"
	"os/exec"
	"testing"
)

func TestRadialOrientationCardinalDirections(t *testing.T) {
	for _, q := range []struct{ yaw, pitch, roll float64 }{{0, -55, 0}, {-55, 0, 90}, {55, 0, -90}, {0, 55, 180}, {0, 0, 0}} {
		v := constrainFisheye(Stream{Yaw: q.yaw, Pitch: q.pitch, Roll: 37, FOV: 65, Width: 960, Height: 720})
		if math.Abs(math.Remainder(v.Roll-q.roll, 360)) > 1e-6 {
			t.Fatalf("yaw/pitch %v/%v got roll %v want %v", q.yaw, q.pitch, v.Roll, q.roll)
		}
	}
}
func TestRadialOrientationFullOrbit(t *testing.T) {
	d := math.Pi / 180
	var firstZ float64
	for a := 0.; a < 360; a += 5 {
		v := Stream{Yaw: math.Atan2(math.Sin(65*d)*math.Cos(a*d), math.Cos(65*d)) / d, Pitch: -math.Asin(math.Sin(65*d)*math.Sin(a*d)) / d, FOV: 85, Width: 960, Height: 720}
		v = constrainFisheye(v)
		x, y, z := streamRay(0, 0, v)
		r := math.Hypot(x, y)
		bx, by, bz := streamRay(0, 1, v)
		dx, dy, dz := bx-x, by-y, bz-z
		length := math.Sqrt(dx*dx + dy*dy + dz*dz)
		dot := (dx*z*x/r + dy*z*y/r - dz*r) / length
		if dot < .999999 {
			t.Fatalf("orbit %.0f: view does not face centre, dot %v", a, dot)
		}
		if a == 0 {
			firstZ = z
		} else if math.Abs(z-firstZ) > 1e-6 {
			t.Fatalf("orbit %.0f asymmetric boundary %v/%v", a, z, firstZ)
		}
		for _, u := range []float64{-1, 1} {
			for _, w := range []float64{-1, 1} {
				_, _, q := streamRay(u, w, v)
				if q < .01199 {
					t.Fatal("outside image circle")
				}
			}
		}
	}
}
func TestRadialActualFFmpegOrbit(t *testing.T) {
	ff := os.Getenv("SPHERELAY_TEST_FFMPEG")
	if ff == "" {
		t.Skip("actual FFmpeg opt-in")
	}
	views := []Stream{}
	for i, p := range [][2]float64{{0, -55}, {-55, 0}, {0, 55}, {55, 0}} {
		views = append(views, Stream{ID: string(rune('a' + i)), Yaw: p[0], Pitch: p[1], FOV: 65, Width: 640, Height: 480, FPS: 5})
	}
	args := ffmpegArgs("unused", "fisheye", views)
	filter := ""
	for i, a := range args {
		if a == "-filter_complex" {
			filter = args[i+1]
		}
	}
	a := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=960x960:rate=5", "-filter_complex", filter}
	for i := range views {
		a = append(a, "-map", "[v"+string(rune('0'+i))+"]", "-frames:v", "1", "-f", "null", "-")
	}
	out, err := exec.CommandContext(context.Background(), ff, a...).CombinedOutput()
	if err != nil {
		t.Fatalf("FFmpeg orbit: %v %s", err, out)
	}
}
