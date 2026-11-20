package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) createMediaConfig() (string, error) {
	s.mu.Lock()
	level := configuredLogLevel(s.store.LogLevels.MediaMTX)
	s.mu.Unlock()
	return s.writeMediaConfig(level)
}

func (s *Server) writeMediaConfig(level string) (string, error) {
	s.mu.Lock()
	netEnabled := s.store.NetworkRTSP
	pass := s.store.ViewerPassword
	s.mu.Unlock()
	addr := "127.0.0.1:8554"
	if netEnabled {
		addr = "0.0.0.0:8554"
	} // Paths are local publisher-only; remote users need the explicit viewer account.
	cfg := fmt.Sprintf(`logLevel: %s
rtsp: true
rtspAddress: %s
rtspTransports: [tcp]
hls: false
webrtc: false
srt: false
api: false
metrics: false
playback: false
authMethod: internal
authInternalUsers:
  - user: any
    pass:
    ips: [127.0.0.1, ::1]
    permissions:
      - action: publish
      - action: read
  - user: viewer
    pass: %s
    ips: []
    permissions:
      - action: read
paths:
  all_others:
    source: publisher
`, level, addr, pass)
	p := filepath.Join(s.dataDir, "mediamtx.yml")
	return p, os.WriteFile(p, []byte(cfg), 0600)
}
func (s *Server) ensureMedia() error {
	s.mu.Lock()
	if s.media != nil && s.media.Cmd != nil && s.media.Cmd.ProcessState == nil {
		s.mu.Unlock()
		return nil
	}
	level := configuredLogLevel(s.store.LogLevels.MediaMTX)
	s.mu.Unlock()
	if !fileExists(s.mediaPath()) {
		s.log("MediaMTX nicht installiert. Über System → Komponenten beziehen.")
		return fmt.Errorf("MediaMTX fehlt")
	}
	conf, err := s.writeMediaConfig(level)
	if err != nil {
		return err
	}
	cmd := backgroundCommand(s.mediaPath(), conf)
	cmd.Dir = s.dataDir
	logfile, _ := os.OpenFile(filepath.Join(s.dataDir, "mediamtx.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	logOutput := &componentLogWriter{file: logfile, event: func(line string) { s.componentDiagnostic("mediamtx", "", line) }}
	if logfile != nil {
		cmd.Stdout = logOutput
		cmd.Stderr = logOutput
	}
	if err := cmd.Start(); err != nil {
		if logfile != nil {
			_ = logfile.Close()
		}
		return err
	}
	p := &Process{Cmd: cmd, Since: time.Now(), Done: make(chan struct{}), LogLevel: level}
	s.mu.Lock()
	s.media = p
	s.mu.Unlock()
	s.log("MediaMTX gestartet")
	go func() {
		e := cmd.Wait()
		if logfile != nil {
			_ = logOutput.flush()
			_ = logfile.Close()
		}
		close(p.Done)
		s.mu.Lock()
		if s.media == p {
			s.media = nil
		}
		s.mu.Unlock()
		if e != nil {
			s.log("MediaMTX beendet: " + e.Error())
		}
	}()
	select {
	case <-p.Done:
		return fmt.Errorf("MediaMTX wurde beim Start beendet. Details stehen im MediaMTX-Protokoll")
	case <-time.After(700 * time.Millisecond):
		return nil
	}
}
func (s *Server) pipelineAction(w http.ResponseWriter, r *http.Request, start bool) {
	var in struct {
		CameraID string `json:"cameraId"`
	}
	if !readReq(w, r, &in) {
		return
	}
	s.mu.Lock()
	if s.updating {
		s.mu.Unlock()
		bad(w, 409, "Ein Neustart oder Komponentenupdate läuft bereits")
		return
	}
	ix := -1
	for i, c := range s.store.Cameras {
		if c.ID == in.CameraID {
			ix = i
			break
		}
	}
	if ix < 0 {
		s.mu.Unlock()
		bad(w, 404, "Kamera nicht gefunden")
		return
	}
	s.store.Cameras[ix].AutoStart = start
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		bad(w, 500, err.Error())
		return
	}
	if !start {
		s.stopPipeline(in.CameraID)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	n, err := s.startCamera(in.CameraID)
	if err != nil {
		bad(w, 412, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "count": n})
}
func (s *Server) startCamera(cameraID string) (int, error) {
	s.mu.Lock()
	var cam Camera
	found := false
	views := []Stream{}
	if p := s.processes[cameraID]; p != nil && p.Cmd != nil && p.Cmd.ProcessState == nil {
		s.mu.Unlock()
		return 0, nil
	}
	for _, c := range s.store.Cameras {
		if c.ID == cameraID {
			cam = c
			found = true
		}
	}
	for _, v := range s.store.Streams {
		if v.CameraID == cameraID && v.Enabled && !v.Paused {
			views = append(views, v)
		}
	}
	s.mu.Unlock()
	if !found {
		return 0, fmt.Errorf("Kamera nicht gefunden")
	}
	if len(views) == 0 {
		return 0, fmt.Errorf("Keine aktiven virtuellen Streams")
	}
	if len(views) > 12 {
		return 0, fmt.Errorf("Maximal 12 Streams pro FFmpeg-Prozess")
	}
	if !fileExists(s.ffmpegPath()) {
		return 0, fmt.Errorf("FFmpeg fehlt")
	}
	if err := s.ensureMedia(); err != nil {
		return 0, err
	}
	original, err := unprotectSecret(cam.Secret)
	if err != nil {
		return 0, fmt.Errorf("Kamera-Zugangsdaten nicht lesbar")
	}
	if original == "" {
		return 0, fmt.Errorf("Kamera-RTSP-Adresse oder Zugangsdaten fehlen")
	}
	options, err := s.preparePipelineOptions(cam, views)
	if err != nil {
		return 0, fmt.Errorf("OSD-/Bildkreisvorbereitung fehlgeschlagen: %w", err)
	}
	args := ffmpegArgs(original, cam.Projection, views, options)
	s.mu.Lock()
	level := configuredLogLevel(s.store.LogLevels.FFmpeg)
	s.mu.Unlock()
	if level != "warn" {
		args[2] = level
		args = append([]string{"-nostats"}, args...)
	}
	cmd := backgroundCommand(s.ffmpegPath(), args...)
	file, e := os.OpenFile(filepath.Join(s.dataDir, "ffmpeg-"+cam.ID+".log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	logOutput := &componentLogWriter{file: file, event: func(line string) { s.componentDiagnostic("ffmpeg", cam.ID, line) }}
	if e == nil {
		cmd.Stdout = logOutput
		cmd.Stderr = logOutput
	}
	s.mu.Lock()
	registered := false
	for _, current := range s.store.Cameras {
		registered = registered || current.ID == cameraID
	}
	if !registered || s.processes[cameraID] != nil {
		s.mu.Unlock()
		if file != nil {
			_ = file.Close()
		}
		if !registered {
			return 0, fmt.Errorf("Kamera nicht gefunden")
		}
		return 0, nil
	}
	if e := cmd.Start(); e != nil {
		s.mu.Unlock()
		if file != nil {
			_ = file.Close()
		}
		return 0, fmt.Errorf("FFmpeg konnte nicht starten: %w", e)
	}
	p := &Process{Cmd: cmd, Since: time.Now(), Done: make(chan struct{}), LogLevel: level, Camera: cam, Streams: views}
	s.processes[cam.ID] = p
	delete(s.cameraRetries, cam.ID)
	s.mu.Unlock()
	s.log("FFmpeg gestartet: " + cam.Name + " (" + strconv.Itoa(len(views)) + " Streams)")
	diagnosticArgs := append([]string(nil), args...)
	for i := 0; i+1 < len(diagnosticArgs); i++ {
		if diagnosticArgs[i] == "-i" {
			diagnosticArgs[i+1] = "[Kameraadresse geschützt]"
		}
	}
	s.recordDiagnostic(diagnosticEvent{Component: "ffmpeg", Level: "info", Camera: cam.ID,
		Message: fmt.Sprintf("Prozess %d gestartet: %s", cmd.Process.Pid, strings.Join(diagnosticArgs, " "))})
	go func() {
		e := cmd.Wait()
		if file != nil {
			_ = logOutput.flush()
			_ = file.Close()
		}
		close(p.Done)
		s.mu.Lock()
		if s.processes[cam.ID] == p {
			delete(s.processes, cam.ID)
			if e != nil {
				s.recordCameraRetryLocked(cam.ID, "Kamera-Pipeline unerwartet beendet")
			}
		}
		s.mu.Unlock()
		if e != nil {
			s.log("FFmpeg beendet: " + cam.Name + "; Details im FFmpeg-Protokoll")
		}
	}()
	return len(views), nil
}
func (s *Server) supervise(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		auto := []string{}
		if !s.updating {
			for _, c := range s.store.Cameras {
				if c.AutoStart && s.processes[c.ID] == nil && s.hasActiveViewsLocked(c.ID) && !time.Now().Before(s.cameraRetries[c.ID].Next) {
					auto = append(auto, c.ID)
				}
			}
		}
		s.mu.Unlock()
		for _, id := range auto {
			if _, err := s.startCamera(id); err != nil {
				s.mu.Lock()
				s.recordCameraRetryLocked(id, err.Error())
				s.mu.Unlock()
				s.recordDiagnostic(diagnosticEvent{Component: "ffmpeg", Level: "warn", Camera: id, Message: "Automatischer Neustart: " + err.Error()})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func ffmpegArgs(source, projection string, views []Stream, configuration ...pipelineOptions) []string {
	options := pipelineOptions{Circle: defaultCircle()}
	if len(configuration) > 0 {
		options = configuration[0]
	}
	n := len(views)
	args := []string{"-hide_banner", "-loglevel", "warning", "-nostdin", "-rtsp_transport", "tcp", "-i", source}
	var f strings.Builder
	maxFPS := 1
	for _, v := range views {
		if v.FPS > maxFPS {
			maxFPS = v.FPS
		}
	}
	crop := ""
	if projection == "fisheye" {
		crop = ",crop='min(iw,ih):min(iw,ih)'"
		if options.Circle.Radius > 0 && options.Circle.Radius < .995 {
			c := options.Circle
			crop += fmt.Sprintf(",crop=iw*%.6f:ih*%.6f:iw*%.6f:ih*%.6f", c.Radius, c.Radius, c.X-c.Radius/2, c.Y-c.Radius/2)
		}
	}
	fmt.Fprintf(&f, "[0:v]fps=%d%s,split=%d", maxFPS, crop, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&f, "[a%d]", i)
	}
	input := "fisheye"
	if projection == "equirect" {
		input = "equirect"
	}
	for i, v := range views {
		if projection == "fisheye" {
			v = constrainFisheye(v)
		}
		fmt.Fprintf(&f, ";[a%d]v360=input=%s:output=flat:interp=lanczos:w=%d:h=%d:yaw=%.4f:pitch=%.4f:roll=%.4f:h_fov=%.6f:v_fov=%.6f,setsar=1,fps=%d%s", i, input, v.Width, v.Height, v.Yaw, v.Pitch, v.Roll, v.FOV, verticalFOV(v), v.FPS, mirrorFilters(v))
		if effectiveOSD(v).Color == "auto" {
			fmt.Fprintf(&f, "[osd%dbase]", i)
			f.WriteString(adaptiveOSDGraph(v, options, i))
		} else {
			fmt.Fprintf(&f, "%s,format=yuv420p[v%d]", osdFilters(v, options), i)
		}
	}
	args = append(args, "-filter_complex", f.String())
	for i, v := range views {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i), "-an", "-c:v", "libx264", "-profile:v", "high", "-preset", "veryfast", "-crf", "18", "-tune", "zerolatency", "-g", strconv.Itoa(v.FPS*2))
		if v.Go1984Compatibility {
			args = append(args, "-x264-params", "threads=1:sliced-threads=0:slices=1", "-pkt_size", "1200")
		}
		args = append(args, "-f", "rtsp", "-rtsp_transport", "tcp", "rtsp://127.0.0.1:8554/"+v.ID)
	}
	return args
}
func (s *Server) stopPipeline(id string) *Process {
	s.mu.Lock()
	p := s.processes[id]
	delete(s.processes, id)
	s.mu.Unlock()
	if p != nil && p.Cmd != nil && p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
		s.log("Stream-Prozess gestoppt: " + id)
	}
	return p
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("camera")
	viewid := r.URL.Query().Get("view")
	s.mu.Lock()
	var cam Camera
	found := false
	var v Stream
	viewFound := viewid == ""
	for _, c := range s.store.Cameras {
		if c.ID == id {
			cam = c
			found = true
		}
	}
	for _, x := range s.store.Streams {
		if x.ID == viewid && x.CameraID == id {
			v = x
			viewFound = true
		}
	}
	key := id + "/" + viewid
	cache, cached := s.cache[key]
	s.mu.Unlock()
	if !found || !viewFound {
		bad(w, 404, "Bildquelle nicht gefunden")
		return
	}
	if cached && time.Since(cache.At) < 3*time.Second {
		if cache.Err != "" {
			bad(w, 502, cache.Err)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(cache.Body)
		return
	}
	if !fileExists(s.ffmpegPath()) {
		bad(w, 412, "FFmpeg fehlt")
		return
	}
	source, err := unprotectSecret(cam.Secret)
	if err != nil {
		bad(w, 500, "Zugangsdaten nicht verfügbar")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp", "-i", source}
	if viewid != "" {
		crop := ""
		if cam.Projection == "fisheye" {
			crop = "crop='min(iw,ih):min(iw,ih)',"
		}
		filter := fmt.Sprintf("%sv360=input=%s:output=flat:yaw=%.2f:pitch=%.2f:h_fov=%.2f:v_fov=%.6f:w=%d:h=%d,setsar=1%s", crop, cam.Projection, v.Yaw, v.Pitch, v.FOV, verticalFOV(v), v.Width, v.Height, mirrorFilters(v))
		args = append(args, "-vf", filter)
	} else {
		args = append(args, "-vf", "scale=720:-2")
	}
	args = append(args, "-frames:v", "1", "-f", "image2", "-c:v", "mjpeg", "-q:v", "5", "pipe:1")
	cmd := backgroundCommandContext(ctx, s.ffmpegPath(), args...)
	output, err := cmd.Output()
	if err != nil {
		bad(w, 502, "Vorschau nicht verfügbar. Kamera/FFmpeg/Projektion prüfen.")
		return
	}
	if len(output) < 4 || len(output) > 8<<20 {
		bad(w, 502, "Ungültiges Vorschaubild")
		return
	}
	s.mu.Lock()
	s.cache[key] = Snapshot{At: time.Now(), Body: output}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(output)
}

var _ = io.Copy

func (s *Server) stopMedia() {
	s.mu.Lock()
	old := s.media
	s.media = nil
	ids := []string{}
	for id := range s.processes {
		if !strings.HasPrefix(id, "install-") {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.stopPipeline(id)
	}
	if old != nil && old.Cmd != nil && old.Cmd.Process != nil {
		_ = old.Cmd.Process.Kill()
	}
	s.log("MediaMTX und Kamerapipelines angehalten")
}
func (s *Server) restartMediaAPI(w http.ResponseWriter, r *http.Request) {
	if !s.beginStreamRestart(w) {
		return
	}
	defer s.endStreamRestart()
	s.mu.Lock()
	media := s.media
	running := make(map[string]*Process)
	for _, camera := range s.store.Cameras {
		if process := s.processes[camera.ID]; process != nil {
			running[camera.ID] = process
		}
	}
	s.mu.Unlock()
	s.stopMedia()
	if err := waitRestartProcess(media); err != nil {
		bad(w, 504, err.Error())
		return
	}
	if err := s.ensureMedia(); err != nil {
		bad(w, 412, err.Error())
		return
	}
	failures := 0
	for id, process := range running {
		if err := waitRestartProcess(process); err != nil {
			failures++
			s.log("FFmpeg nach MediaMTX-Neustart: " + err.Error())
			continue
		}
		if _, err := s.startCamera(id); err != nil {
			failures++
			s.log("FFmpeg nach MediaMTX-Neustart: " + err.Error())
		}
	}
	message := "MediaMTX neu gestartet."
	if failures > 0 {
		message += fmt.Sprintf(" %d Kamera-Pipeline(s) konnten nicht wieder gestartet werden.", failures)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": message})
}

func (s *Server) beginStreamRestart(w http.ResponseWriter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updating {
		bad(w, 409, "Ein Neustart oder Komponentenupdate läuft bereits")
		return false
	}
	s.updating = true
	return true
}

func (s *Server) endStreamRestart() {
	s.mu.Lock()
	s.updating = false
	s.mu.Unlock()
}

func waitRestartProcess(process *Process) error {
	if process == nil || process.Done == nil {
		return nil
	}
	select {
	case <-process.Done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("Der bisherige Prozess wurde nicht rechtzeitig beendet")
	}
}

func (s *Server) restartPipelineAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CameraID string `json:"cameraId"`
	}
	if !readReq(w, r, &in) || !s.beginStreamRestart(w) {
		return
	}
	defer s.endStreamRestart()
	s.mu.Lock()
	found := false
	for _, camera := range s.store.Cameras {
		if camera.ID == in.CameraID {
			found = true
			break
		}
	}
	active := s.hasActiveViewsLocked(in.CameraID)
	s.mu.Unlock()
	if !found {
		bad(w, 404, "Kamera nicht gefunden")
		return
	}
	if err := waitRestartProcess(s.stopPipeline(in.CameraID)); err != nil {
		bad(w, 504, err.Error())
		return
	}
	if !active {
		writeJSON(w, 200, map[string]any{"ok": true, "message": "Kamera-Pipeline gestoppt. Keine Ansicht ist aktiviert."})
		return
	}
	if _, err := s.startCamera(in.CameraID); err != nil {
		bad(w, 412, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "FFmpeg für die ausgewählte Kamera neu gestartet."})
}
