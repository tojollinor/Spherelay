package main

import (
	"cmp"
	"net/http"
	"net/url"
	"reflect"
	"time"
)

type cameraRetry struct {
	Next   time.Time
	Delay  time.Duration
	Reason string
}

func (s *Server) recordCameraRetryLocked(id, reason string) {
	if s.cameraRetries == nil {
		s.cameraRetries = make(map[string]cameraRetry)
	}
	delay := min(time.Minute, max(15*time.Second, 2*s.cameraRetries[id].Delay))
	s.cameraRetries[id] = cameraRetry{time.Now().Add(delay), delay, reason}
}

func (s *Server) hasActiveViewsLocked(id string) bool {
	for _, view := range s.store.Streams {
		if view.CameraID == id && view.Enabled && !view.Paused {
			return true
		}
	}
	return false
}

func (s *Server) cameraRestartPendingLocked(camera Camera) bool {
	process := s.processes[camera.ID]
	if process == nil {
		return false
	}
	if process.Camera != camera || process.LogLevel != configuredLogLevel(s.store.LogLevels.FFmpeg) {
		return true
	}
	views := make([]Stream, 0, len(process.Streams))
	for _, view := range s.store.Streams {
		if view.CameraID == camera.ID && view.Enabled && !view.Paused {
			views = append(views, view)
		}
	}
	return !reflect.DeepEqual(process.Streams, views)
}

func (s *Server) streamControlAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID      string `json:"id"`
		Action  string `json:"action"`
		Enabled bool   `json:"enabled"`
	}
	if !readReq(w, r, &input) {
		return
	}
	switch input.Action {
	case "start", "stop", "restart", "enabled":
	default:
		bad(w, 400, "Unbekannte Streamaktion")
		return
	}
	if !s.beginStreamRestart(w) {
		return
	}
	defer s.endStreamRestart()
	s.mu.Lock()
	index, cameraIndex := -1, -1
	for i, view := range s.store.Streams {
		if view.ID == input.ID {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		bad(w, 404, "Ansicht nicht gefunden")
		return
	}
	previous := s.store.Streams[index]
	id := previous.CameraID
	for i, camera := range s.store.Cameras {
		if camera.ID == id {
			cameraIndex = i
			break
		}
	}
	if cameraIndex < 0 {
		s.mu.Unlock()
		bad(w, 404, "Kamera nicht gefunden")
		return
	}
	camera := s.store.Cameras[cameraIndex]
	running := s.processes[id] != nil
	switch input.Action {
	case "enabled":
		s.store.Streams[index].Enabled = input.Enabled
	case "start", "restart":
		if !previous.Enabled {
			s.mu.Unlock()
			bad(w, 409, "Ansicht zuerst aktivieren")
			return
		}
		s.store.Streams[index].Paused = false
		s.store.Cameras[cameraIndex].AutoStart = true
	case "stop":
		s.store.Streams[index].Paused = true
	}
	if err := s.saveLocked(); err != nil {
		s.store.Streams[index], s.store.Cameras[cameraIndex] = previous, camera
		s.mu.Unlock()
		bad(w, 500, err.Error())
		return
	}
	active := s.hasActiveViewsLocked(id)
	if input.Action == "enabled" {
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true, "cameraId": id, "restartRequired": running})
		return
	}
	s.mu.Unlock()
	if err := waitRestartProcess(s.stopPipeline(id)); err != nil {
		bad(w, 504, err.Error())
		return
	}
	if active && (running || input.Action != "stop") {
		if _, err := s.startCamera(id); err != nil {
			s.mu.Lock()
			s.recordCameraRetryLocked(id, err.Error())
			s.mu.Unlock()
			bad(w, 412, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "cameraId": id})
}

func (s *Server) cameraConnectionTestAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if !readReq(w, r, &input) {
		return
	}
	raw := input.URL
	if raw == "" && input.ID != "" {
		s.mu.Lock()
		for _, camera := range s.store.Cameras {
			if camera.ID == input.ID {
				raw, _ = unprotectSecret(camera.Secret)
				break
			}
		}
		s.mu.Unlock()
	}
	address, err := url.Parse(raw)
	if err != nil {
		bad(w, 400, "Ungültige RTSP-Adresse")
		return
	}
	var creds *DiscoveryCredentials
	if address.User != nil {
		password, _ := address.User.Password()
		creds = &DiscoveryCredentials{Username: address.User.Username(), Password: password}
		address.User = nil
	}
	ready, detail := verifyCameraRTSP(r.Context(), address.String(), creds)
	writeJSON(w, 200, map[string]any{"ok": ready, "detail": cmp.Or(detail, "RTSP-Verbindung konnte nicht bestätigt werden")})
}
