package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type componentRelease struct{ Version, URL, SHA string }
type ComponentStatus struct {
	Name             string            `json:"name"`
	Installed        bool              `json:"installed"`
	InstalledVersion string            `json:"installedVersion"`
	LatestVersion    string            `json:"latestVersion,omitempty"`
	UpdateAvailable  bool              `json:"updateAvailable"`
	Status           string            `json:"status"`
	Phase            string            `json:"phase"`
	Message          string            `json:"message,omitempty"`
	Received         int64             `json:"received"`
	Total            int64             `json:"total"`
	FinishedAt       time.Time         `json:"finishedAt,omitempty"`
	Release          *componentRelease `json:"-"`
}

var versionNumbers = regexp.MustCompile(`(?i)v?(\d+)\.(\d+)\.(\d+)`)

func normalizedVersion(value string) string {
	match := versionNumbers.FindStringSubmatch(value)
	if len(match) < 4 {
		return strings.TrimSpace(value)
	}
	return strings.Join(match[1:4], ".")
}
func newerVersion(latest, installed string) bool {
	a, b := versionNumbers.FindStringSubmatch(latest), versionNumbers.FindStringSubmatch(installed)
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return false
}

func (s *Server) localComponentVersion(name string) (bool, string) {
	path, arg := s.ffmpegPath(), "-version"
	if name == "mediamtx" {
		path, arg = s.mediaPath(), "--version"
	}
	if !fileExists(path) {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := backgroundCommandContext(ctx, path, arg).Output()
	if err != nil {
		return true, "Version unbekannt"
	}
	return true, normalizedVersion(string(output))
}

func latestComponentRelease(ctx context.Context, name string) (*componentRelease, error) {
	if name == "ffmpeg" {
		bytes, err := fetchBounded(ctx, "https://www.gyan.dev/ffmpeg/builds/release-version", 4096)
		if err != nil {
			return nil, err
		}
		version := normalizedVersion(string(bytes))
		if !versionNumbers.MatchString(version) {
			return nil, fmt.Errorf("FFmpeg-Versionsangabe nicht lesbar")
		}
		url := "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
		sha, err := checksumOf(ctx, url+".sha256")
		if err != nil {
			return nil, err
		}
		return &componentRelease{version, url, sha}, nil
	}
	bytes, err := fetchBounded(ctx, "https://api.github.com/repos/bluenviron/mediamtx/releases/latest", 2<<20)
	if err != nil {
		return nil, err
	}
	var meta struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if json.Unmarshal(bytes, &meta) != nil {
		return nil, fmt.Errorf("MediaMTX-Release nicht lesbar")
	}
	for _, asset := range meta.Assets {
		if strings.Contains(asset.Name, "windows_amd64") && strings.HasSuffix(asset.Name, ".zip") && strings.HasPrefix(asset.URL, "https://github.com/bluenviron/mediamtx/releases/download/") && strings.HasPrefix(asset.Digest, "sha256:") {
			return &componentRelease{normalizedVersion(meta.Tag), asset.URL, strings.TrimPrefix(asset.Digest, "sha256:")}, nil
		}
	}
	return nil, fmt.Errorf("Kein überprüfbares MediaMTX-Windows-Release gefunden")
}

func (s *Server) setComponentPhase(name, phase string, received, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status := s.components[name]; status != nil {
		status.Phase = phase
		status.Received = received
		status.Total = total
	}
}

func (s *Server) componentsAPI(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"ffmpeg", "mediamtx"} {
		s.mu.Lock()
		missing := s.components == nil || s.components[name] == nil
		s.mu.Unlock()
		if missing {
			installed, version := s.localComponentVersion(name)
			s.mu.Lock()
			if s.components == nil {
				s.components = map[string]*ComponentStatus{}
			}
			if s.components[name] == nil {
				s.components[name] = &ComponentStatus{Name: name, Installed: installed, InstalledVersion: version, Status: "idle", Phase: "Bereit"}
			}
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	items := []ComponentStatus{*s.components["ffmpeg"], *s.components["mediamtx"]}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{"components": items})
}

func (s *Server) checkComponent(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	if s.components == nil {
		s.components = map[string]*ComponentStatus{}
	}
	current := s.components[name]
	if current != nil && (current.Status == "checking" || current.Status == "installing") {
		s.mu.Unlock()
		bad(w, 409, "Für diese Komponente läuft bereits ein Vorgang")
		return
	}
	state := &ComponentStatus{Name: name, Status: "checking", Phase: "Version wird geprüft"}
	if current != nil {
		state.Installed = current.Installed
		state.InstalledVersion = current.InstalledVersion
		state.LatestVersion = current.LatestVersion
	}
	s.components[name] = state
	s.mu.Unlock()
	go func() {
		installed, version := s.localComponentVersion(name)
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		release, err := latestComponentRelease(ctx, name)
		s.mu.Lock()
		defer s.mu.Unlock()
		state := s.components[name]
		state.Installed = installed
		state.InstalledVersion = version
		state.FinishedAt = time.Now()
		if err != nil {
			state.Status = "error"
			state.Phase = "Prüfung fehlgeschlagen"
			state.Message = err.Error()
			return
		}
		state.Release = release
		state.LatestVersion = release.Version
		state.UpdateAvailable = installed && newerVersion(release.Version, version)
		state.Status = "ready"
		if !installed {
			state.Phase = "Zur Installation bereit"
		} else if state.UpdateAvailable {
			state.Phase = "Update verfügbar"
		} else if version == "Version unbekannt" {
			state.Phase = "Installierte Version nicht vergleichbar"
		} else {
			state.Phase = "Auf dem aktuellen Stand"
		}
	}()
	writeJSON(w, 202, map[string]any{"ok": true, "message": "Versionsprüfung gestartet"})
}

func (s *Server) downloadComponentArchive(ctx context.Context, name, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	// Large archives must not inherit the short metadata request deadline.
	archiveClient := *downloadClient
	archiveClient.Timeout = 30 * time.Minute
	res, err := archiveClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if res.ContentLength > limit {
		return nil, fmt.Errorf("Komponentenarchiv zu groß")
	}
	s.setComponentPhase(name, "Wird heruntergeladen", 0, res.ContentLength)
	data := make([]byte, 0)
	chunk := make([]byte, 64<<10)
	last := time.Now()
	for {
		n, e := res.Body.Read(chunk)
		if n > 0 {
			data = append(data, chunk[:n]...)
			if int64(len(data)) > limit {
				return nil, fmt.Errorf("Komponentenarchiv zu groß")
			}
			if time.Since(last) > 200*time.Millisecond {
				s.setComponentPhase(name, "Wird heruntergeladen", int64(len(data)), res.ContentLength)
				last = time.Now()
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
	}
	s.setComponentPhase(name, "SHA256 wird geprüft", int64(len(data)), res.ContentLength)
	return data, nil
}
