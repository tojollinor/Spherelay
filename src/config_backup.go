package main

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type configurationBackup struct {
	Format       int                `json:"format"`
	Cameras      []Camera           `json:"cameras"`
	Streams      []Stream           `json:"streams"`
	NetworkRTSP  bool               `json:"networkRtsp"`
	NetworkONVIF bool               `json:"networkOnvif"`
	LogLevels    componentLogLevels `json:"logLevels"`
}

var backupID = regexp.MustCompile(`^(cam|view)-[A-Za-z0-9_-]{1,43}$`)

func backupCameraURL(value string) string {
	u, err := url.Parse(value)
	if err != nil || !validRTSP(value) {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func (s *Server) configurationBackupAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	result := configurationBackup{Format: 1, Cameras: append([]Camera{}, s.store.Cameras...), Streams: append([]Stream{}, s.store.Streams...),
		NetworkRTSP: s.store.NetworkRTSP, NetworkONVIF: s.store.NetworkONVIF, LogLevels: s.store.LogLevels}
	s.mu.Unlock()
	for i := range result.Cameras {
		result.Cameras[i].Secret = ""
		result.Cameras[i].URL = backupCameraURL(result.Cameras[i].URL)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="spherelay-konfiguration.json"`)
	writeJSON(w, 200, result)
}

func (s *Server) configurationRestoreAPI(w http.ResponseWriter, r *http.Request) {
	var input configurationBackup
	if !readReq(w, r, &input) {
		return
	}
	if input.Format != 1 || input.NetworkONVIF && !input.NetworkRTSP || len(input.Cameras) > 512 || len(input.Streams) > 2048 {
		bad(w, 400, "Ungültiges oder nicht unterstütztes Sicherungsformat")
		return
	}
	known := make(map[string]bool, len(input.Cameras)+len(input.Streams))
	for _, camera := range input.Cameras {
		if !backupID.MatchString(camera.ID) || !strings.HasPrefix(camera.ID, "cam-") || known[camera.ID] ||
			strings.TrimSpace(camera.Name) == "" || len(camera.Name) > 80 || camera.Secret != "" ||
			(camera.Projection != "fisheye" && camera.Projection != "equirect") ||
			(camera.URL != "" && backupCameraURL(camera.URL) != camera.URL) {
			bad(w, 400, "Ungültige Kamera in der Sicherung")
			return
		}
		known[camera.ID] = true
	}
	for _, view := range input.Streams {
		if !backupID.MatchString(view.ID) || !strings.HasPrefix(view.ID, "view-") || !strings.HasPrefix(view.CameraID, "cam-") ||
			known[view.ID] || !known[view.CameraID] || !validStreamSettings(view) {
			bad(w, 400, "Ungültige Ansicht in der Sicherung")
			return
		}
		known[view.ID] = true
	}
	if input.LogLevels.MediaMTX != "" && !validLogLevel(input.LogLevels.MediaMTX) ||
		input.LogLevels.FFmpeg != "" && !validLogLevel(input.LogLevels.FFmpeg) {
		bad(w, 400, "Ungültige Protokollstufe in der Sicherung")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updating {
		bad(w, 409, "Bitte laufende Arbeiten zuerst abschließen")
		return
	}
	old := s.store
	credentials := make(map[string]Camera, len(old.Cameras))
	for _, camera := range old.Cameras {
		credentials[camera.ID] = camera
	}
	missing := 0
	for i := range input.Cameras {
		camera := &input.Cameras[i]
		current := credentials[camera.ID]
		if current.Secret != "" && backupCameraURL(current.URL) == camera.URL {
			camera.Secret, camera.URL = current.Secret, current.URL
		} else {
			camera.AutoStart = false
			missing++
		}
	}
	s.store.Cameras, s.store.Streams = input.Cameras, input.Streams
	s.store.NetworkRTSP, s.store.NetworkONVIF = input.NetworkRTSP, input.NetworkONVIF
	s.store.LogLevels = input.LogLevels
	if err := s.saveLocked(); err != nil {
		s.store = old
		bad(w, 500, "Sicherung konnte nicht gespeichert werden")
		return
	}
	s.networkRestartPending = true
	writeJSON(w, 200, map[string]any{"restartRequired": true, "missingCredentials": missing})
}
