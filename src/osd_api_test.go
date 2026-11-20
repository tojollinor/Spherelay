package main

import (
	"net/http"
	"testing"
	"time"
)

func TestOSDSettingsSaveRoundTrip(t *testing.T) {
	s, e := newServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.store.Cameras = []Camera{{ID: "cam-test", Name: "Camera 01", Projection: "fisheye"}}
	s.sessions["test-session"] = Session{Token: "test-session", Expires: time.Now().Add(time.Minute)}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test-session"}
	margin := 0.
	v := Stream{CameraID: "cam-test", Name: "Camera 01", Width: 1280, Height: 720, FOV: 80, FPS: 5, Enabled: true, OSD: &OSDConfig{ViewName: true, Clock: true, TitlePosition: "bottom-center", ClockPosition: "top-right", ClockFormat: "iso", SizePercent: 3.5, MarginPercent: &margin, Color: "#000000", Font: "sans", Style: "box"}}
	w := tRequest(t, s, "POST", "/api/streams", v, cookie)
	if w.Code != 200 {
		t.Fatalf("save %d", w.Code)
	}
	loaded, e := newServer(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	o := effectiveOSD(loaded.store.Streams[0])
	if o.TitlePosition != "bottom-center" || o.ClockPosition != "top-right" || o.ClockFormat != "iso" || o.SizePercent != 3.5 || *o.MarginPercent != 0 || o.Color != "#000000" || o.Font != "sans" || o.Style != "box" {
		t.Fatalf("round trip mismatch %+v", o)
	}
	v.OSD.Color = "white:reload=1"
	if w = tRequest(t, s, "POST", "/api/streams", v, cookie); w.Code != 400 {
		t.Fatal("invalid OSD accepted by API")
	}
	v.ID = loaded.store.Streams[0].ID
	v.OSD.Color = "auto"
	if w = tRequest(t, s, "POST", "/api/streams", v, cookie); w.Code != 200 {
		t.Fatal("automatic colour not accepted")
	}
	loaded, e = newServer(s.dataDir)
	if e != nil || loaded.store.Streams[0].OSD.Color != "auto" {
		t.Fatal("automatic colour not persisted")
	}
	for _, fov := range []float64{15, 160, 14, 161} {
		v.FOV = fov
		w = tRequest(t, s, "POST", "/api/streams", v, cookie)
		if (fov == 15 || fov == 160) != (w.Code == 200) {
			t.Fatalf("zoom FOV %.0f API %d", fov, w.Code)
		}
	}

}
