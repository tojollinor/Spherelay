package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var downloadClient = &http.Client{Timeout: 4 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 5 {
		return fmt.Errorf("Zu viele Weiterleitungen")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("Unsichere Weiterleitung")
	}
	return nil
}}

func (s *Server) installComponent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Component string `json:"component"`
		Action    string `json:"action"`
	}
	if !readReq(w, r, &in) {
		return
	}
	if runtime.GOOS != "windows" {
		bad(w, 400, "Komponenteninstaller unterstützt nur Windows")
		return
	}
	if in.Component != "ffmpeg" && in.Component != "mediamtx" {
		bad(w, 400, "Unbekannte Komponente")
		return
	}
	if in.Action == "check" {
		s.checkComponent(w, r, in.Component)
		return
	}
	if in.Action != "" && in.Action != "install" {
		bad(w, 400, "Unbekannte Komponentenaktion")
		return
	}
	s.mu.Lock()
	busy := s.updating
	if state := s.components[in.Component]; state != nil && state.Status == "checking" {
		busy = true
	}
	if in.Component == "ffmpeg" && (len(s.processes) > 0 || len(s.previews) > 0) {
		busy = true
	}
	if busy {
		s.mu.Unlock()
		bad(w, 409, "Laufende Prozesse zuerst stoppen, bevor diese Komponente aktualisiert wird")
		return
	}
	s.updating = true
	if s.components == nil {
		s.components = map[string]*ComponentStatus{}
	}
	previous := s.components[in.Component]
	status := &ComponentStatus{Name: in.Component, Status: "installing", Phase: "Installation wird vorbereitet"}
	if previous != nil {
		status.Release = previous.Release
		status.LatestVersion = previous.LatestVersion
		status.InstalledVersion = previous.InstalledVersion
		status.Installed = previous.Installed
	}
	s.components[in.Component] = status
	s.processes["install-"+in.Component] = &Process{Since: time.Now()}
	s.mu.Unlock()
	go func() {
		err := s.fetchComponent(in.Component)
		if err == nil && in.Component == "mediamtx" {
			err = s.ensureMedia()
		}
		installed, installedVersion := s.localComponentVersion(in.Component)
		s.mu.Lock()
		delete(s.processes, "install-"+in.Component)
		s.updating = false
		state := s.components[in.Component]
		state.Installed = installed
		state.InstalledVersion = installedVersion
		state.FinishedAt = time.Now()
		state.Received = 0
		state.Total = 0
		if err != nil {
			state.Status = "error"
			state.Phase = "Vorgang fehlgeschlagen"
			state.Message = err.Error()
		} else {
			state.Status = "ready"
			state.Phase = "Fertig"
			state.Message = "Komponente ist einsatzbereit"
			state.UpdateAvailable = false
		}
		s.mu.Unlock()
		if err != nil {
			s.log("Komponente " + in.Component + ": " + err.Error())
		} else {
			s.log("Komponente erfolgreich bereitgestellt: " + in.Component)
		}
	}()
	writeJSON(w, 202, map[string]any{"ok": true, "message": "Download und Integritätsprüfung gestartet. Status im Diagnoseprotokoll."})
}
func (s *Server) fetchComponent(c string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s.mu.Lock()
	var release *componentRelease
	if state := s.components[c]; state != nil {
		release = state.Release
	}
	s.mu.Unlock()
	var err error
	if release == nil {
		s.setComponentPhase(c, "Verfügbare Version wird ermittelt", 0, 0)
		release, err = latestComponentRelease(ctx, c)
	}
	if err != nil {
		return err
	}
	asset, sha := release.URL, release.SHA
	installed, installedVersion := s.localComponentVersion(c)
	s.mu.Lock()
	if state := s.components[c]; state != nil {
		state.Release = release
		state.LatestVersion = release.Version
	}
	s.mu.Unlock()
	if installed && installedVersion == release.Version {
		return nil
	}
	if installed && versionNumbers.MatchString(installedVersion) && !newerVersion(release.Version, installedVersion) {
		return fmt.Errorf("Keine neuere Version verfügbar")
	}
	if len(sha) != 64 {
		return fmt.Errorf("Keine überprüfbare SHA256-Prüfsumme vorhanden")
	}
	s.log("Lade " + c + " aus offizieller Quelle")
	data, err := s.downloadComponentArchive(ctx, c, asset, 260<<20)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != strings.ToLower(sha) {
		return fmt.Errorf("SHA256-Prüfsumme stimmt nicht überein")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	s.setComponentPhase(c, "Archiv wird entpackt", 0, 0)
	needle := c + ".exe"
	var binary []byte
	for _, f := range z.File {
		if strings.EqualFold(filepath.Base(f.Name), needle) {
			if f.UncompressedSize64 > 150<<20 {
				return fmt.Errorf("Komponente zu groß")
			}
			rc, e := f.Open()
			if e != nil {
				return e
			}
			binary, e = io.ReadAll(io.LimitReader(rc, 150<<20))
			_ = rc.Close()
			if e != nil {
				return e
			}
			break
		}
	}
	if len(binary) < 1024 {
		return fmt.Errorf("%s wurde im Archiv nicht gefunden", needle)
	}
	target := s.mediaPath()
	if c == "ffmpeg" {
		target = s.ffmpegPath()
	}
	tmp := target + ".new.exe"
	if err := os.WriteFile(tmp, binary, 0700); err != nil {
		return err
	}
	vctx, cancelv := context.WithTimeout(ctx, 10*time.Second)
	defer cancelv()
	command := backgroundCommandContext(vctx, tmp, "-version")
	if c == "mediamtx" {
		command = backgroundCommandContext(vctx, tmp, "--version")
	}
	s.setComponentPhase(c, "Programmversion wird geprüft", 0, 0)
	versionOutput, versionErr := command.Output()
	if versionErr != nil || normalizedVersion(string(versionOutput)) != release.Version {
		_ = os.Remove(tmp)
		return fmt.Errorf("Heruntergeladene Programmversion stimmt nicht mit der geprüften Version überein")
	}
	if c == "mediamtx" {
		s.setComponentPhase(c, "MediaMTX wird für das Update angehalten", 0, 0)
		s.stopMedia()
	}
	s.setComponentPhase(c, "Programm wird installiert", 0, 0)
	backup := target + ".previous"
	_ = os.Remove(backup)
	if fileExists(target) {
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		if fileExists(backup) {
			_ = os.Rename(backup, target)
		}
		return err
	}
	return nil
}
func fetchBounded(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", u, nil)
	if e != nil {
		return nil, e
	}
	res, e := downloadClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if res.ContentLength > limit {
		return nil, fmt.Errorf("Download überschreitet Größenlimit")
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("Download zu groß")
	}
	return body, nil
}
func checksumOf(ctx context.Context, u string) (string, error) {
	b, e := fetchBounded(ctx, u, 16000)
	if e != nil {
		return "", e
	}
	re := regexp.MustCompile(`(?i)[a-f0-9]{64}`)
	v := re.Find(b)
	if v == nil {
		return "", fmt.Errorf("SHA256-Datei enthält keinen gültigen Hash")
	}
	return strings.ToLower(string(v)), nil
}
func mediaRelease(ctx context.Context) (string, string, error) {
	data, err := fetchBounded(ctx, "https://api.github.com/repos/bluenviron/mediamtx/releases/latest", 2<<20)
	if err != nil {
		return "", "", err
	}
	var meta struct {
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if e := json.Unmarshal(data, &meta); e != nil {
		return "", "", e
	}
	for _, a := range meta.Assets {
		if strings.Contains(a.Name, "windows_amd64") && strings.HasSuffix(a.Name, ".zip") && strings.HasPrefix(a.URL, "https://github.com/bluenviron/mediamtx/releases/download/") {
			if strings.HasPrefix(a.Digest, "sha256:") {
				return a.URL, strings.TrimPrefix(a.Digest, "sha256:"), nil
			}
			return "", "", fmt.Errorf("Release enthält keinen geprüften SHA256-Digest")
		}
	}
	return "", "", fmt.Errorf("Kein Windows x64-Release gefunden")
}
