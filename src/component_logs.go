package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type componentLogLevels struct {
	MediaMTX string `json:"mediamtx"`
	FFmpeg   string `json:"ffmpeg"`
}

func validLogLevel(level string) bool {
	return level == "warn" || level == "info" || level == "debug"
}

func configuredLogLevel(level string) string {
	if validLogLevel(level) {
		return level
	}
	return "warn"
}

var (
	logURLUserinfo = regexp.MustCompile(`(?i)((?:rtsp|rtsps|https?)://)[^\r\n/?#]*@`)
	logRTSPHeader  = regexp.MustCompile(`(?im)((?:proxy-)?authorization|cookie|set-cookie):[^\r\n]*`)
	logQuerySecret = regexp.MustCompile(`(?i)([?&](?:password|pass|pwd|user|username|token|secret|key|api[_-]?key)=)[^\s&"'<>]*`)
)

// Both command output streams share this writer. os/exec serializes writes
// when Stdout and Stderr refer to the same comparable writer.
type componentLogWriter struct {
	file       io.Writer
	event      func(string)
	line       []byte
	overflowed bool
}

func (log *componentLogWriter) Write(data []byte) (int, error) {
	length := len(data)
	for len(data) > 0 {
		line, rest, complete := bytes.Cut(data, []byte("\n"))
		if !log.overflowed && len(log.line)+len(line) <= 64*1024 {
			log.line = append(log.line, line...)
		} else {
			log.overflowed = true
		}
		if complete {
			if err := log.flush(); err != nil {
				return 0, err
			}
		}
		data = rest
	}
	return length, nil
}

func (log *componentLogWriter) flush() error {
	if len(log.line) == 0 && !log.overflowed {
		return nil
	}
	line := "[Überlange Protokollzeile ausgelassen]"
	if !log.overflowed {
		line = redactComponentLog(string(log.line))
	}
	log.line = log.line[:0]
	log.overflowed = false
	if log.event != nil {
		log.event(line)
	}
	_, err := io.WriteString(log.file, line+"\n")
	return err
}

func redactComponentLog(value string) string {
	value = logURLUserinfo.ReplaceAllString(value, "${1}[geschützt]@")
	value = logRTSPHeader.ReplaceAllString(value, "${1}: [geschützt]")
	return logQuerySecret.ReplaceAllString(value, "${1}[geschützt]")
}

func componentLogTail(path string) (string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	const limit = 64 * 1024
	start := max(0, info.Size()-limit)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, info.Size()-start))
	if err != nil {
		return "", err
	}
	value := string(data)
	if start > 0 {
		_, remainder, _ := strings.Cut(value, "\n")
		value = remainder
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	return redactComponentLog(value), nil
}

func activeLogLevel(process *Process) string {
	if process == nil {
		return ""
	}
	return configuredLogLevel(process.LogLevel)
}

func (s *Server) componentLogsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var levels componentLogLevels
		if !readReq(w, r, &levels) {
			return
		}
		if (levels.MediaMTX == "" && levels.FFmpeg == "") ||
			(levels.MediaMTX != "" && !validLogLevel(levels.MediaMTX)) ||
			(levels.FFmpeg != "" && !validLogLevel(levels.FFmpeg)) {
			bad(w, 400, "Ungültige Logstufe")
			return
		}
		s.mu.Lock()
		previous := s.store.LogLevels
		if levels.MediaMTX == "" {
			levels.MediaMTX = configuredLogLevel(previous.MediaMTX)
		}
		if levels.FFmpeg == "" {
			levels.FFmpeg = configuredLogLevel(previous.FFmpeg)
		}
		s.store.LogLevels = levels
		err := s.saveLocked()
		if err != nil {
			s.store.LogLevels = previous
		}
		s.mu.Unlock()
		if err != nil {
			bad(w, 500, "Logstufen konnten nicht gespeichert werden")
			return
		}
		writeJSON(w, 200, levels)
		return
	}

	cameraID := r.URL.Query().Get("cameraId")
	component := r.URL.Query().Get("component")
	s.mu.Lock()
	levels := componentLogLevels{configuredLogLevel(s.store.LogLevels.MediaMTX), configuredLogLevel(s.store.LogLevels.FFmpeg)}
	mediaActive := activeLogLevel(s.media)
	pipelines := make(map[string]string)
	cameraFound := false
	for _, camera := range s.store.Cameras {
		cameraFound = cameraFound || camera.ID == cameraID
		if level := activeLogLevel(s.processes[camera.ID]); level != "" {
			pipelines[camera.ID] = level
		}
	}
	s.mu.Unlock()

	file := ""
	switch component {
	case "mediamtx":
		file = "mediamtx.log"
	case "ffmpeg":
		if cameraFound && cameraID != "" && filepath.Base(cameraID) == cameraID && !strings.ContainsAny(cameraID, `/\:`) {
			file = "ffmpeg-" + cameraID + ".log"
		}
	}
	content := ""
	if file != "" {
		var err error
		content, err = componentLogTail(filepath.Join(s.dataDir, file))
		if err != nil {
			bad(w, 500, "Protokoll konnte nicht gelesen werden")
			return
		}
	}
	writeJSON(w, 200, map[string]any{"levels": levels, "mediaActive": mediaActive, "pipelines": pipelines, "content": content})
}
