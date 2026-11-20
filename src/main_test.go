package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGo1984CompatibilityOnlyAppliesToSelectedView(t *testing.T) {
	compatible := Stream{ID: "view-compatible", FPS: 15, Width: 864, Height: 480, FOV: 75, Go1984Compatibility: true}
	standard := compatible
	standard.ID, standard.Go1984Compatibility = "view-standard", false
	args := strings.Join(ffmpegArgs("unused", "equirect", []Stream{compatible, standard}), " ")
	first, second, found := strings.Cut(args, "/view-compatible ")
	if !found || !strings.Contains(first, "-g 30 -x264-params threads=1:sliced-threads=0:slices=1 -pkt_size 1200") {
		t.Fatal("tested encoder settings are missing from the compatible output")
	}
	if strings.Contains(second, "-x264-params") || strings.Contains(second, "-pkt_size") || !strings.Contains(second, "-g 30") {
		t.Fatal("compatibility settings affected the standard output")
	}
}

func tRequest(t *testing.T, s *Server, method, path string, data any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if data != nil {
		body, _ = json.Marshal(data)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40999"
	if method != "GET" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Spherelay-Request", "1")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.onHTTP(w, r)
	return w
}
func TestEndToEndAPI(t *testing.T) {
	s, err := newServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := tRequest(t, s, "POST", "/api/setup", map[string]any{"password": "really-long-test-password"}, nil)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	w = tRequest(t, s, "POST", "/api/login", map[string]any{"password": "really-long-test-password"}, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	w = tRequest(t, s, "POST", "/api/cameras", map[string]any{"name": "Testkamera", "url": "rtsp://cam:secret@10.0.0.1/Streaming/Channels/101", "projection": "fisheye"}, cookie)
	if w.Code != 200 {
		t.Fatalf("camera: %d %s", w.Code, w.Body.String())
	}
	var cr struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cr)
	w = tRequest(t, s, "GET", "/api/state", nil, cookie)
	if w.Code != 200 {
		t.Fatalf("state: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret@") {
		t.Fatalf("credential leak in API response: %s", w.Body.String())
	}
	if cr.ID == "" {
		t.Fatal("camera ID empty")
	}
	w = tRequest(t, s, "POST", "/api/streams", map[string]any{"cameraId": cr.ID, "name": "Box 1", "yaw": 20, "pitch": -15, "fov": 80, "width": 960, "height": 720, "fps": 10, "enabled": true}, cookie)
	if w.Code != 200 {
		t.Fatalf("stream: %d %s", w.Code, w.Body.String())
	}
	s2, err := newServer(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.store.Cameras) != 1 || len(s2.store.Streams) != 1 {
		t.Fatal("configuration not persisted")
	}
	original, err := unprotectSecret(s2.store.Cameras[0].Secret)
	if err != nil || !strings.Contains(original, "secret@") {
		t.Fatal("secret cannot be recovered")
	}
	w = tRequest(t, s, "DELETE", "/api/cameras/"+cr.ID, nil, cookie)
	if w.Code != 200 {
		t.Fatalf("delete camera: %d %s", w.Code, w.Body.String())
	}
	s2, err = newServer(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.store.Cameras) != 0 || len(s2.store.Streams) != 0 {
		t.Fatal("camera and its views were not removed from persisted configuration")
	}
}
func TestAuthRejectsUnauthenticatedAndNoCSRF(t *testing.T) {
	s, _ := newServer(t.TempDir())
	w := tRequest(t, s, "GET", "/api/state", nil, nil)
	if w.Code != 401 {
		t.Fatalf("expected unauthorized, got %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/setup", strings.NewReader(`{"password":"123456789012"}`))
	r.RemoteAddr = "127.0.0.1:3"
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.onHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
func TestFFmpegArgumentsFourViews(t *testing.T) {
	vs := []Stream{}
	for i := 0; i < 4; i++ {
		vs = append(vs, Stream{ID: "view-abcd", Width: 960, Height: 720, FPS: 10, FOV: 80, Yaw: float64(i) * 30})
	}
	args := ffmpegArgs("rtsp://example.com/test", "fisheye", vs)
	got := strings.Join(args, " ")
	if strings.Count(got, "v360=input=fisheye") != 4 || strings.Count(got, "rtsp://127.0.0.1:8554/") != 4 || !strings.Contains(got, "split=4") {
		t.Fatalf("invalid pipeline: %s", got)
	}
}
func TestRTSPValidation(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://example.com", "rtsp://", "rtsp://example.com/ok"} {
		valid := validRTSP(raw)
		if valid != (strings.HasPrefix(raw, "rtsp://example.com")) {
			t.Fatalf("unexpected result %s: %v", raw, valid)
		}
	}
}
