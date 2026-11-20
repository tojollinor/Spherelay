package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type cameraPreview struct {
	mu       sync.Mutex
	frame    []byte
	sequence uint64
	at       time.Time
	changed  chan struct{}
	failure  string
	finished bool
	users    int
	lastUse  time.Time
	cancel   context.CancelFunc
	circle   LensCircle
}

func (p *cameraPreview) notify() { close(p.changed); p.changed = make(chan struct{}) }
func (p *cameraPreview) publish(frame []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frame = frame
	p.sequence++
	p.at = time.Now()
	p.failure = ""
	p.notify()
}
func (p *cameraPreview) finish(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
	p.failure = message
	p.notify()
}

// FFmpeg produces JPEG-only frames without embedded thumbnails. Bound all buffering.
func readPreviewFrames(reader io.Reader, publish func([]byte)) error {
	buffer := []byte{}
	chunk := make([]byte, 32<<10)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)
		}
		for {
			start := bytes.Index(buffer, []byte{0xff, 0xd8})
			if start < 0 {
				if len(buffer) > 0 {
					buffer = buffer[len(buffer)-1:]
				}
				break
			}
			if start > 0 {
				buffer = buffer[start:]
			}
			end := bytes.Index(buffer[2:], []byte{0xff, 0xd9})
			if end < 0 {
				break
			}
			end += 4
			frame := append([]byte(nil), buffer[:end]...)
			buffer = buffer[end:]
			publish(frame)
		}
		if len(buffer) > 8<<20 {
			return fmt.Errorf("Vorschaubild überschreitet Größenlimit")
		}
		if err != nil {
			return err
		}
	}
}

func previewArgs(source, projection string) []string {
	filter := "fps=4,scale='min(iw,2048)':-2:flags=lanczos"
	if projection == "fisheye" {
		filter = "fps=4,crop='min(iw,ih):min(iw,ih)',scale='min(iw,2048)':-2:flags=lanczos"
	}
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-rtsp_transport", "tcp", "-timeout", "8000000", "-i", source, "-an", "-vf", filter, "-c:v", "mjpeg", "-q:v", "2", "-f", "image2pipe", "pipe:1"}
}

func (s *Server) acquirePreview(id string) (*cameraPreview, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var camera *Camera
	for _, c := range s.store.Cameras {
		if c.ID == id {
			copy := c
			camera = &copy
			break
		}
	}
	if camera == nil {
		return nil, nil, fmt.Errorf("Kamera nicht gefunden")
	}
	if !fileExists(s.ffmpegPath()) {
		return nil, nil, fmt.Errorf("Für die Vorschau fehlt FFmpeg. Unter System → FFmpeg installieren.")
	}
	if s.updating {
		return nil, nil, fmt.Errorf("Komponenten werden aktualisiert; Vorschau anschließend neu starten")
	}
	if s.previews == nil {
		s.previews = map[string]*cameraPreview{}
	}
	p := s.previews[id]
	if p != nil {
		p.mu.Lock()
		finished := p.finished
		p.mu.Unlock()
		if finished {
			p = nil
		}
	}
	if p == nil {
		source, err := unprotectSecret(camera.Secret)
		if err != nil {
			return nil, nil, fmt.Errorf("Kamerazugang nicht verfügbar")
		}
		ctx, cancel := context.WithCancel(context.Background())
		p = &cameraPreview{changed: make(chan struct{}), cancel: cancel, lastUse: time.Now()}
		s.previews[id] = p
		go s.runCameraPreview(ctx, id, p, source, camera.Projection)
	}
	p.mu.Lock()
	p.users++
	p.lastUse = time.Now()
	p.mu.Unlock()
	release := func() { p.mu.Lock(); p.users--; p.lastUse = time.Now(); p.mu.Unlock() }
	return p, release, nil
}

func (s *Server) runCameraPreview(ctx context.Context, id string, p *cameraPreview, source, projection string) {
	defer func() {
		p.cancel()
		s.mu.Lock()
		if s.previews[id] == p {
			delete(s.previews, id)
		}
		s.mu.Unlock()
	}()
	cmd := backgroundCommandContext(ctx, s.ffmpegPath(), previewArgs(source, projection)...)
	output, err := cmd.StdoutPipe()
	if err != nil {
		p.finish("Vorschau konnte nicht geöffnet werden")
		return
	}
	// No raw stderr is returned or logged: FFmpeg can include source credentials.
	cmd.Stderr = io.Discard
	if cmd.Start() != nil {
		p.finish("FFmpeg konnte für die Vorschau nicht starten")
		return
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.mu.Lock()
				idle := p.users == 0 && time.Since(p.lastUse) > 8*time.Second
				stalled := p.users > 0 && ((p.at.IsZero() && time.Since(p.lastUse) > 15*time.Second) || (!p.at.IsZero() && time.Since(p.at) > 12*time.Second))
				p.mu.Unlock()
				if idle || stalled {
					p.cancel()
					return
				}
			}
		}
	}()
	readPreviewFrames(output, func(frame []byte) { s.rememberCircle(id, p, frame); p.publish(frame) })
	err = cmd.Wait()
	if ctx.Err() != nil {
		p.finish("Kameravorschau beendet oder Kamera antwortet nicht")
	} else if err != nil {
		p.finish("Kein Kamerabild empfangen. Kamerazugang, RTSP-Stream und FFmpeg prüfen.")
	} else {
		p.finish("Kameravorschau beendet")
	}
}

func (s *Server) previewFrame(w http.ResponseWriter, r *http.Request) {
	p, release, err := s.acquirePreview(r.URL.Query().Get("camera"))
	if err != nil {
		bad(w, 412, err.Error())
		return
	}
	defer release()
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	timer := time.NewTimer(18 * time.Second)
	defer timer.Stop()
	for {
		p.mu.Lock()
		frame, sequence, at, changed, failure, finished := p.frame, p.sequence, p.at, p.changed, p.failure, p.finished
		p.mu.Unlock()
		if finished {
			bad(w, 502, failure)
			return
		}
		if len(frame) > 0 && sequence != after {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Spherelay-Frame", strconv.FormatUint(sequence, 10))
			w.Header().Set("X-Spherelay-Frame-Time", strconv.FormatInt(at.UnixMilli(), 10))
			p.mu.Lock()
			circle := p.circle
			p.mu.Unlock()
			w.Header().Set("X-Spherelay-Circle", fmt.Sprintf("%.6f,%.6f,%.6f", circle.X, circle.Y, circle.Radius))
			w.Write(frame)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			bad(w, 504, "Die Kamera liefert derzeit kein neues Vorschaubild")
			return
		case <-changed:
		}
	}
}

func (s *Server) stopPreviews() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, preview := range s.previews {
		preview.cancel()
	}
}
