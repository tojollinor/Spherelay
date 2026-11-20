package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type fragmentedPreviewReader struct{ data []byte }

func (r *fragmentedPreviewReader) Read(b []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := 1
	if len(b) < n {
		n = len(b)
	}
	copy(b, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestPreviewJPEGFraming(t *testing.T) {
	var input bytes.Buffer
	image := image.NewRGBA(image.Rect(0, 0, 4, 3))
	image.Set(1, 1, color.RGBA{R: 255, A: 255})
	jpeg.Encode(&input, image, nil)
	frame := input.Bytes()
	stream := append([]byte("leading bytes"), frame...)
	stream = append(stream, frame...)
	frames := [][]byte{}
	err := readPreviewFrames(&fragmentedPreviewReader{data: stream}, func(data []byte) { frames = append(frames, data) })
	if err != io.EOF || len(frames) != 2 || !bytes.Equal(frames[0], frame) || !bytes.Equal(frames[1], frame) {
		t.Fatal("fragmented JPEG stream framing failed")
	}
	if _, err := jpeg.Decode(bytes.NewReader(frames[0])); err != nil {
		t.Fatal("framed JPEG is not decodable")
	}
	oversized := append([]byte{0xff, 0xd8}, bytes.Repeat([]byte{1}, (8<<20)+1)...)
	if err := readPreviewFrames(bytes.NewReader(oversized), func([]byte) {}); err == nil || err == io.EOF {
		t.Fatal("unbounded frame accepted")
	}
}

func TestPreviewFrameWaitsForNewFrameAndRequiresLogin(t *testing.T) {
	s, _ := newServer(t.TempDir())
	p := &cameraPreview{changed: make(chan struct{}), cancel: func() {}}
	s.store.Cameras = []Camera{{ID: "camera-demo"}}
	os.WriteFile(s.ffmpegPath(), []byte("fixture"), 0700)
	s.previews = map[string]*cameraPreview{"camera-demo": p}
	if result := tRequest(t, s, "GET", "/api/preview/frame?camera=camera-demo", nil, nil); result.Code != 401 {
		t.Fatal("preview must require login")
	}
	p.publish([]byte("frame-one"))
	releaseDone := make(chan struct{})
	go func() { time.Sleep(30 * time.Millisecond); p.publish([]byte("frame-two")); close(releaseDone) }()
	request := httptest.NewRequest("GET", "/api/preview/frame?camera=camera-demo&after=1", nil)
	result := httptest.NewRecorder()
	s.previewFrame(result, request)
	<-releaseDone
	if result.Code != 200 || result.Header().Get("X-Spherelay-Frame") != "2" || result.Body.String() != "frame-two" {
		t.Fatalf("new frame not delivered: %d %s", result.Code, result.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = httptest.NewRecorder()
	s.previewFrame(result, request.WithContext(ctx))
	if p.users != 0 {
		t.Fatal("preview subscriber not released")
	}
}

func TestPreviewArgumentsMatchProjectionAndSingleOutput(t *testing.T) {
	args := strings.Join(previewArgs("rtsp://192.168.1.1/demo", "fisheye"), " ")
	if !strings.Contains(args, "fps=4,crop=") || !strings.Contains(args, "min(iw,2048)") || !strings.Contains(args, "image2pipe") || strings.Contains(args, "mediamtx") {
		t.Fatal("unexpected preview decoding pipeline")
	}
	if strings.Contains(strings.Join(previewArgs("rtsp://192.168.1.1/demo", "equirect"), " "), "crop=") {
		t.Fatal("panorama must not be cropped into a circle")
	}
}

func TestLiveCameraPreviewFrames(t *testing.T) {
	if os.Getenv("SPHERELAY_PREVIEW_LIVE") != "1" {
		t.Skip("live camera preview diagnostic disabled")
	}
	s, err := newServer(os.Getenv("SPHERELAY_DIAGNOSTIC_DATA"))
	if err != nil {
		t.Fatal("data directory unavailable")
	}
	if len(s.store.Cameras) == 0 {
		t.Fatal("no configured camera")
	}
	p, release, err := s.acquirePreview(s.store.Cameras[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	defer p.cancel()
	timeout := time.NewTimer(25 * time.Second)
	defer timeout.Stop()
	sequence := uint64(0)
	count := 0
	for count < 3 {
		p.mu.Lock()
		frame, n, changed, failure, finished := p.frame, p.sequence, p.changed, p.failure, p.finished
		p.mu.Unlock()
		if finished {
			t.Fatal(failure)
		}
		if n > sequence && len(frame) > 0 {
			config, err := jpeg.DecodeConfig(bytes.NewReader(frame))
			if err != nil {
				t.Fatal("live frame not a valid JPEG")
			}
			if config.Width <= 0 || config.Width > 2048 {
				t.Fatalf("unexpected preview size %dx%d", config.Width, config.Height)
			}
			sequence = n
			count++
			t.Logf("Real preview frame %d: %dx%d, %d bytes", n, config.Width, config.Height, len(frame))
			if count == 1 && os.Getenv("SPHERELAY_DIAGNOSTIC_REPORT") != "" {
				os.WriteFile(os.Getenv("SPHERELAY_DIAGNOSTIC_REPORT"), frame, 0600)
			}
			continue
		}
		select {
		case <-timeout.C:
			t.Fatal("no continuing camera preview")
		case <-changed:
		}
	}
	t.Logf("Continuing real camera preview confirmed with %d frames; one shared decoder", count)
}
