package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const streamProbeTimeout = 2 * time.Second

type streamStatus struct {
	ID              string     `json:"id"`
	Address         string     `json:"address"`
	PipelineRunning bool       `json:"pipelineRunning"`
	Online          bool       `json:"online"`
	Detail          string     `json:"detail"`
	CheckedAt       time.Time  `json:"checkedAt"`
	State           string     `json:"state"`
	CameraID        string     `json:"cameraId"`
	Enabled         bool       `json:"enabled"`
	Paused          bool       `json:"paused"`
	LastOnline      *time.Time `json:"lastOnline,omitempty"`
	NextRetry       *time.Time `json:"nextRetry,omitempty"`
	SourceError     string     `json:"sourceError,omitempty"`
}

func (s *Server) streamStatusAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"streams": s.collectStreamStatuses(r.Context())})
}

func (s *Server) collectStreamStatuses(ctx context.Context) []streamStatus {
	s.mu.Lock()
	cameras := make(map[string]Camera, len(s.store.Cameras))
	for _, camera := range s.store.Cameras {
		cameras[camera.ID] = camera
	}
	results := make([]streamStatus, 0, len(s.store.Streams))
	for _, stream := range s.store.Streams {
		p := s.processes[stream.CameraID]
		result := streamStatus{
			ID: stream.ID, Address: "rtsp://127.0.0.1:8554/" + url.PathEscape(stream.ID),
			PipelineRunning: p != nil, CameraID: stream.CameraID, Enabled: stream.Enabled, Paused: stream.Paused,
			State: "offline", Detail: "Stream gestoppt",
		}
		if previous := s.streamLastOnline[stream.ID]; !previous.IsZero() {
			result.LastOnline = &previous
		}
		desired := stream.Enabled && !stream.Paused
		if p != nil {
			for _, published := range p.Streams {
				if published.ID == stream.ID {
					desired = true
					break
				}
			}
		}
		switch {
		case desired && p != nil:
			result.State = "unhealthy"
		case desired && cameras[stream.CameraID].AutoStart:
			result.State, result.Detail = "unhealthy", "Kamera-Pipeline nicht gestartet"
			retry := s.cameraRetries[stream.CameraID]
			result.SourceError = retry.Reason
			if !retry.Next.IsZero() {
				result.NextRetry = &retry.Next
			}
		case !stream.Enabled:
			result.Detail = "Ansicht deaktiviert"
		}
		results = append(results, result)
	}
	s.mu.Unlock()

	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(results)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				result := &results[i]
				result.CheckedAt = time.Now()
				if result.State == "offline" || !result.PipelineRunning {
					continue
				}
				result.Detail = probeRTSP(ctx, "127.0.0.1:8554", result.Address)
				result.Online = result.Detail == ""
				result.CheckedAt = time.Now()
				if result.Online {
					result.State = "online"
					result.LastOnline = &result.CheckedAt
					s.mu.Lock()
					if s.streamLastOnline == nil {
						s.streamLastOnline = make(map[string]time.Time)
					}
					if result.CheckedAt.After(s.streamLastOnline[result.ID]) {
						s.streamLastOnline[result.ID] = result.CheckedAt
					}
					s.mu.Unlock()
				}
			}
		}()
	}
	for i := range results {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return results
}

func probeRTSP(parent context.Context, host, address string) string {
	ctx, cancel := context.WithTimeout(parent, streamProbeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
	if err != nil {
		return "RTSP-Server nicht erreichbar"
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	return describeRTSP(conn, address)
}

func describeRTSP(conn io.ReadWriter, address string) string {
	if _, err := fmt.Fprintf(conn, "DESCRIBE %s RTSP/1.0\r\nCSeq: 1\r\nAccept: application/sdp\r\n\r\n", address); err != nil {
		return "RTSP-Anfrage fehlgeschlagen"
	}
	reader := bufio.NewReader(io.LimitReader(conn, 64*1024))
	response := textproto.NewReader(reader)
	line, err := response.ReadLine()
	if err != nil {
		return "Keine RTSP-Antwort erhalten"
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "RTSP/1.0" {
		return "Ungültige RTSP-Antwort"
	}
	if fields[1] == "404" {
		return "Unter dieser Adresse wird kein Stream angeboten"
	}
	if fields[1] != "200" {
		return "RTSP-Antwort: " + fields[1]
	}
	headers, err := response.ReadMIMEHeader()
	if err != nil || !strings.EqualFold(headers.Get("Content-Type"), "application/sdp") {
		return "Keine gültige Streambeschreibung erhalten"
	}
	length, err := strconv.Atoi(headers.Get("Content-Length"))
	if err != nil || length <= 0 || length > 32*1024 {
		return "Ungültige Länge der Streambeschreibung"
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return "Streambeschreibung unvollständig"
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "m=video ") {
			return ""
		}
	}
	return "Streambeschreibung enthält keine Videospur"
}
