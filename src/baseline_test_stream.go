package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

var baselineSourceID = regexp.MustCompile(`^view-[A-Za-z0-9_-]{1,43}$`)

func baselineTestArgs(id string, fps int) []string {
	base := "rtsp://" + rtspPlayTestUpstream + "/"
	return []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin", "-rtsp_transport", "tcp",
		"-i", base + id, "-map", "0:v:0", "-an",
		"-c:v", "libx264", "-profile:v", "baseline", "-pix_fmt", "yuv420p",
		"-preset", "veryfast", "-crf", "18", "-tune", "zerolatency", "-g", strconv.Itoa(fps * 2),
		"-f", "rtsp", "-rtsp_transport", "tcp", base + id + "-baseline",
	}
}

func (s *Server) baselineTestAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID    string `json:"id"`
		Start bool   `json:"start"`
	}
	if !readReq(w, r, &input) {
		return
	}
	if !baselineSourceID.MatchString(input.ID) {
		bad(w, 400, "Ungültige Stream-ID")
		return
	}
	key := "baseline-" + input.ID
	if !input.Start {
		if err := waitRestartProcess(s.stopPipeline(key)); err != nil {
			bad(w, 504, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"running": false})
		return
	}

	s.mu.Lock()
	var view Stream
	for _, candidate := range s.store.Streams {
		if candidate.ID == input.ID+"-baseline" {
			s.mu.Unlock()
			bad(w, 409, "Die Testadresse wird bereits von einer Ansicht verwendet")
			return
		}
		if candidate.ID == input.ID {
			view = candidate
		}
	}
	if s.updating || view.ID == "" || !view.Enabled || s.processes[view.CameraID] == nil {
		s.mu.Unlock()
		bad(w, 409, "Für den Baseline-Test muss der normale Stream laufen")
		return
	}
	if s.processes[key] != nil {
		s.mu.Unlock()
		writeJSON(w, 200, map[string]bool{"running": true})
		return
	}
	file, err := os.OpenFile(filepath.Join(s.dataDir, "ffmpeg-"+key+".log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		s.mu.Unlock()
		bad(w, 500, "Baseline-FFmpeg-Protokoll konnte nicht geöffnet werden")
		return
	}
	output := &componentLogWriter{file: file, event: func(line string) { s.componentDiagnostic("ffmpeg", view.CameraID, line) }}
	cmd := backgroundCommand(s.ffmpegPath(), baselineTestArgs(view.ID, view.FPS)...)
	cmd.Stderr, cmd.Stdout = output, output
	if err := cmd.Start(); err != nil {
		_ = file.Close()
		s.mu.Unlock()
		bad(w, 412, fmt.Sprintf("Baseline-FFmpeg konnte nicht starten: %v", err))
		return
	}
	process := &Process{Cmd: cmd, Since: time.Now(), Done: make(chan struct{})}
	s.processes[key] = process
	s.mu.Unlock()
	go func() {
		err := cmd.Wait()
		_ = output.flush()
		_ = file.Close()
		s.mu.Lock()
		current := s.processes[key] == process
		if current {
			delete(s.processes, key)
		}
		s.mu.Unlock()
		close(process.Done)
		if current && err != nil {
			s.log("Baseline-Test beendet: " + view.ID + "; Details in ffmpeg-" + key + ".log")
		}
	}()
	s.log("H.264-Baseline-Test gestartet: " + view.ID)
	writeJSON(w, 200, map[string]bool{"running": true})
}
