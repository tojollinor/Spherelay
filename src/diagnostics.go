package main

import (
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type diagnosticEvent struct {
	ID         uint64     `json:"id"`
	Time       time.Time  `json:"time"`
	LastTime   *time.Time `json:"lastTime,omitempty"`
	Count      int        `json:"count,omitempty"`
	Component  string     `json:"component"`
	Level      string     `json:"level"`
	Message    string     `json:"message"`
	Camera     string     `json:"camera,omitempty"`
	Stream     string     `json:"stream,omitempty"`
	Connection string     `json:"connection,omitempty"`
	Status     string     `json:"status,omitempty"`
}

var diagnosticMediaVersion = regexp.MustCompile(`MediaMTX v([0-9][0-9A-Za-z.+-]*)`)
var diagnosticMediaSession = regexp.MustCompile(`\[session ([^\]]+)\]`)
var diagnosticLoopbackPeer = regexp.MustCompile(`127\.0\.0\.1:\d+`)

func correlateMediaDiagnostics(events []diagnosticEvent) {
	peers := make(map[string]diagnosticEvent)
	for _, event := range events {
		if event.Component != "rtsp" || !strings.Contains(event.Message, "MediaMTX-Verbindung ") {
			continue
		}
		_, addresses, _ := strings.Cut(event.Message, "MediaMTX-Verbindung ")
		if peer := diagnosticLoopbackPeer.FindString(addresses); peer != "" {
			peers[peer] = event
		}
	}
	metadata := make(map[string]diagnosticEvent)
	for _, event := range events {
		if event.Connection != "" && event.Stream != "" {
			metadata[event.Connection] = event
		}
	}
	sessions := make(map[string]diagnosticEvent)
	for i := range events {
		event := &events[i]
		if event.Component != "mediamtx" {
			continue
		}
		peer := diagnosticLoopbackPeer.FindString(event.Message)
		link := peers[peer]
		if match := diagnosticMediaSession.FindStringSubmatch(event.Message); match != nil {
			if link.Connection != "" {
				sessions[match[1]] = link
			} else {
				link = sessions[match[1]]
			}
		}
		event.Connection = link.Connection
		event.Stream = metadata[link.Connection].Stream
	}
}

func (s *Server) componentDiagnostic(component, camera, line string) {
	level := "info"
	switch {
	case strings.Contains(line, " CRI "):
		level = "critical"
	case strings.Contains(line, " ERR "), strings.Contains(line, "Error"), strings.Contains(line, "failed"):
		level = "error"
	case strings.Contains(line, " WAR "), strings.Contains(line, "reader is too slow"):
		level = "warn"
	case strings.Contains(line, " DEB "):
		level = "debug"
	}
	s.recordDiagnostic(diagnosticEvent{Component: component, Level: level, Camera: camera, Message: line})
}

func (s *Server) diagnosticsAPI(w http.ResponseWriter, r *http.Request) {
	s.logmu.Lock()
	events := append([]diagnosticEvent{}, s.diagnosticEvents...)
	s.logmu.Unlock()
	correlateMediaDiagnostics(events)
	type streamState struct {
		ID              string `json:"id"`
		Camera          string `json:"camera"`
		Name            string `json:"name"`
		Enabled         bool   `json:"enabled"`
		PipelineRunning bool   `json:"pipelineRunning"`
		Codec           string `json:"codec"`
		Width           int    `json:"width"`
		Height          int    `json:"height"`
		FPS             int    `json:"fps"`
	}
	streams := make([]streamState, 0)
	s.mu.Lock()
	for _, view := range s.store.Streams {
		streams = append(streams, streamState{view.ID, view.CameraID, view.Name, view.Enabled, s.processes[view.CameraID] != nil, "H264", view.Width, view.Height, view.FPS})
	}
	mediaVersion := ""
	if component := s.components["mediamtx"]; component != nil {
		mediaVersion = component.InstalledVersion
	}
	onvifLAN := s.onvifLAN
	configuredONVIF := s.store.NetworkONVIF
	mediaRunning := s.media != nil
	s.mu.Unlock()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Component != "mediamtx" {
			continue
		}
		if match := diagnosticMediaVersion.FindStringSubmatch(events[i].Message); match != nil {
			mediaVersion = match[1]
			break
		}
	}
	writeJSON(w, 200, map[string]any{
		"generatedAt": time.Now(), "serviceStarted": s.started, "version": version, "platform": runtime.GOOS, "architecture": runtime.GOARCH,
		"mediaMTXVersion": mediaVersion, "mediaMTXRunning": mediaRunning, "onvifLAN": onvifLAN, "onvifConfigured": configuredONVIF,
		"streams": streams, "events": events,
	})
}
