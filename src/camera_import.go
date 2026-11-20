package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type cachedInspection struct {
	Details CameraDetails
	Expires time.Time
}

func (s *Server) cacheInspection(details *CameraDetails) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inspections == nil {
		s.inspections = map[string]cachedInspection{}
	}
	for key, value := range s.inspections {
		if time.Now().After(value.Expires) {
			delete(s.inspections, key)
		}
	}
	if len(s.inspections) >= 128 {
		for key := range s.inspections {
			delete(s.inspections, key)
			break
		}
	}
	details.InspectionID = token(18)
	s.inspections[details.InspectionID] = cachedInspection{Details: *details, Expires: time.Now().Add(20 * time.Minute)}
}

func (s *Server) cameraDetailsAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Base         string `json:"base"`
		CredentialID string `json:"credentialId"`
	}
	if !readReq(w, r, &in) {
		return
	}
	s.mu.Lock()
	profile, err := s.selectCredentialProfile(in.CredentialID)
	s.mu.Unlock()
	if err != nil {
		bad(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	details, err := inspectCamera(ctx, in.Base, &profile.DiscoveryCredentials, profile.ID)
	if err != nil {
		bad(w, 400, err.Error())
		return
	}
	s.cacheInspection(&details)
	writeJSON(w, 200, details)
}

func (s *Server) importCameraSourceAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InspectionID string `json:"inspectionId"`
		SourceID     string `json:"sourceId"`
		Name         string `json:"name"`
		Projection   string `json:"projection"`
	}
	if !readReq(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || (in.Projection != "fisheye" && in.Projection != "equirect") {
		bad(w, 400, "Kameraname und Quellprojektion prüfen")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inspection, ok := s.inspections[in.InspectionID]
	if !ok || time.Now().After(inspection.Expires) {
		bad(w, 400, "Geräteinformationen erneut abrufen")
		return
	}
	profile, err := s.selectCredentialProfile(inspection.Details.CredentialID)
	if err != nil {
		bad(w, 400, err.Error())
		return
	}
	raw := ""
	for _, source := range inspection.Details.Sources {
		if source.ID == in.SourceID {
			raw = source.URL
			break
		}
	}
	if raw == "" {
		bad(w, 400, "Ermittelten Stream auswählen")
		return
	}
	for _, camera := range s.store.Cameras {
		if camera.URL == raw {
			bad(w, 409, "Dieser Quellstream ist bereits als Kamera hinterlegt")
			return
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !validRTSP(raw) || u.Hostname() != inspection.Details.Address {
		bad(w, 400, "Ungültiger ermittelter Stream")
		return
	}
	u.User = url.UserPassword(profile.Username, profile.Password)
	secret, err := protectSecret(u.String())
	if err != nil {
		bad(w, 500, "Kamera konnte nicht verschlüsselt gespeichert werden")
		return
	}
	camera := Camera{ID: "cam-" + token(6), Name: in.Name, URL: raw, Secret: secret, Projection: in.Projection, Enabled: true, AutoStart: false}
	s.store.Cameras = append(s.store.Cameras, camera)
	if err := s.saveLocked(); err != nil {
		s.store.Cameras = s.store.Cameras[:len(s.store.Cameras)-1]
		bad(w, 500, "Kamera konnte nicht gespeichert werden")
		return
	}
	writeJSON(w, 200, map[string]any{"id": camera.ID})
}
