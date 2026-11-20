//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

func windowsUpdateFolder(kind uintptr) (string, error) {
	var folder [260]uint16
	// CSIDL_PROGRAM_FILES is resolved by Windows, independently of the process environment.
	status, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetFolderPathW").Call(
		0, kind, 0, 0, uintptr(unsafe.Pointer(&folder[0])),
	)
	if status != 0 {
		return "", fmt.Errorf("Windows-Programmordner konnte nicht ermittelt werden")
	}
	return syscall.UTF16ToString(folder[:]), nil
}
func installedAppUpdatePath() (string, error) {
	folder, err := windowsUpdateFolder(0x26)
	if err != nil {
		return "", err
	}
	path := filepath.Join(folder, "Spherelay", "Spherelay.exe")
	info, err := readSpherelayService()
	prefix := `"` + path + `" --service`
	if err != nil || !info.Installed ||
		!(strings.EqualFold(info.BinaryPath, prefix) || strings.HasPrefix(strings.ToLower(info.BinaryPath), strings.ToLower(prefix+" "))) {
		return "", fmt.Errorf("Online-Updates benötigen die installierte Windows-Dienstversion von Spherelay")
	}
	return path, nil
}

func updateReadyViews() []appUpdateView {
	folder, err := windowsUpdateFolder(0x23)
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(folder, "Spherelay", "settings.json"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var settings Store
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&settings) != nil {
		return nil
	}
	views := make([]appUpdateView, 0, len(settings.Streams))
	for _, view := range settings.Streams {
		if view.Enabled && backupID.MatchString(view.ID) {
			views = append(views, appUpdateView{ID: view.ID, Name: view.Name})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probeUpdateViews(ctx, views)
	online := views[:0]
	for _, view := range views {
		if view.Online {
			online = append(online, view)
		}
	}
	return online
}

func probeUpdateViews(ctx context.Context, views []appUpdateView) {
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(views)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				v := &views[index]
				if !v.Online && ctx.Err() == nil {
					v.Online = probeRTSP(ctx, "127.0.0.1:8554", "rtsp://127.0.0.1:8554/"+url.PathEscape(v.ID)) == ""
				}
			}
		}()
	}
	for i := range views {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
}

func writeAppUpdateResult(installed, staging, target string, views []appUpdateView) {
	for i := range views {
		views[i].Online = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		probeUpdateViews(ctx, views)
		pending := false
		for _, view := range views {
			pending = pending || !view.Online
		}
		if !pending || ctx.Err() != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	report := appUpdateResult{Version: target, CheckedAt: time.Now(), ServiceReady: true, Streams: views}
	data, err := json.Marshal(report)
	if err != nil {
		return
	}
	file := filepath.Join(staging, "update-result.json")
	if os.WriteFile(file, data, 0600) == nil {
		_ = replaceFile(file, filepath.Join(filepath.Dir(installed), "Spherelay.update.json"))
	}
	_ = os.Remove(file)
}

func installerUpdateSnapshot(path string) error {
	data, err := json.Marshal(updateReadyViews())
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func installerUpdateReport(path string) error {
	installed, err := installedAppUpdatePath()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var views []appUpdateView
	if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&views); err != nil {
		return err
	}
	for _, view := range views {
		if !backupID.MatchString(view.ID) {
			return fmt.Errorf("Ungültiger Stream in der Installationsprüfung")
		}
	}
	if err = waitAppVersion(version); err != nil {
		return err
	}
	writeAppUpdateResult(installed, filepath.Dir(path), version, views)
	return nil
}

func canRunWindowsAppUpdate() bool {
	installed, err := installedAppUpdatePath()
	if err != nil {
		return false
	}
	running, err := os.Executable()
	return err == nil && strings.EqualFold(filepath.Clean(running), installed)
}

func appUpdateProtocolVersion(raw string) (string, error) {
	uri, err := url.Parse(raw)
	if err != nil || uri.Scheme != "spherelay" || uri.Host != "update" || uri.User != nil ||
		uri.ForceQuery || uri.RawQuery != "" || uri.Fragment != "" || uri.RawPath != "" {
		return "", fmt.Errorf("Ungültige Spherelay-Updateadresse")
	}
	target := strings.TrimPrefix(uri.Path, "/")
	if !appReleaseVersion.MatchString(target) {
		return "", fmt.Errorf("Ungültige Spherelay-Updateversion")
	}
	return target, nil
}

func requestAppUpdate(target string) error {
	installed, err := installedAppUpdatePath()
	if err != nil {
		return err
	}
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0,
		uintptr(unsafe.Pointer(winText("runas"))), uintptr(unsafe.Pointer(winText(installed))),
		uintptr(unsafe.Pointer(winText("--app-update-admin "+target))), 0, 1)
	if result <= 32 {
		return fmt.Errorf("Windows-Update wurde nicht gestartet oder abgebrochen")
	}
	return nil
}

func prepareAppUpdate(ctx context.Context, release appRelease, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return err
	}
	response, err := appUpdateClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub-Download fehlgeschlagen (HTTP %d)", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	err = copyVerifiedAppUpdate(file, response.Body, release)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	check, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := backgroundCommandContext(check, destination, "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != release.Version {
		return fmt.Errorf("Programmversion der heruntergeladenen Datei stimmt nicht überein")
	}
	return nil
}

func waitAppVersion(expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := http.Client{
		Timeout:       time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8787/api/info", nil)
		if err != nil {
			return err
		}
		if response, err := client.Do(req); err == nil {
			var info struct {
				Version string
				Setup   bool
			}
			err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&info)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && err == nil && info.Version == expected && info.Setup {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Version %s meldet sich nach dem Start nicht bereit", expected)
		case <-ticker.C:
		}
	}
}

func applyWindowsAppUpdate(target string) error {
	if !appReleaseVersion.MatchString(target) || !newerVersion(target, version) || !canRunWindowsAppUpdate() {
		return fmt.Errorf("Dieses Update passt nicht zur installierten Spherelay-Version")
	}
	installed, err := installedAppUpdatePath()
	if err != nil {
		return err
	}
	mutex, _, result := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW").Call(
		0, 0, uintptr(unsafe.Pointer(winText(`Global\SpherelayAppUpdate`))),
	)
	if mutex == 0 {
		return fmt.Errorf("Windows-Updatesperre konnte nicht geöffnet werden: %w", result)
	}
	defer syscall.CloseHandle(syscall.Handle(mutex))
	if result == syscall.Errno(183) {
		return fmt.Errorf("Eine Spherelay-Aktualisierung läuft bereits")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	release, err := checkAppRelease(ctx)
	if err != nil {
		return err
	}
	if release.Version != target || release.URL == "" {
		return fmt.Errorf("Das geprüfte Update ist nicht mehr verfügbar. Bitte erneut nach Updates suchen")
	}
	stage, err := os.MkdirTemp(filepath.Dir(installed), "update-")
	if err != nil {
		return err
	}
	defer os.Remove(stage)
	next := filepath.Join(stage, "next.exe")
	defer os.Remove(next)
	if err = prepareAppUpdate(ctx, release, next); err != nil {
		return err
	}
	previouslyOnline := updateReadyViews()
	previous := filepath.Join(stage, "Spherelay.previous.exe")
	if err = controlSpherelayService("stop"); err != nil {
		return err
	}
	if err = os.Rename(installed, previous); err != nil {
		return errors.Join(err, controlSpherelayService("start"))
	}
	cause := os.Rename(next, installed)
	if cause == nil {
		cause = controlSpherelayService("start")
		if cause == nil {
			cause = waitAppVersion(target)
		}
		if cause == nil {
			writeAppUpdateResult(installed, stage, target, previouslyOnline)
			// The updater or an open tray can still hold this old executable.
			moveFile.Call(uintptr(unsafe.Pointer(winText(previous))), 0, 4)
			moveFile.Call(uintptr(unsafe.Pointer(winText(stage))), 0, 4)
			return nil
		}
		if err := controlSpherelayService("stop"); err != nil {
			return fmt.Errorf("Update fehlgeschlagen; Sicherung in %s: %w", previous, errors.Join(cause, err))
		}
		if err := os.Rename(installed, next); err != nil {
			return fmt.Errorf("Update fehlgeschlagen; Sicherung in %s: %w", previous, errors.Join(cause, err))
		}
	}
	if err := os.Rename(previous, installed); err != nil {
		return fmt.Errorf("Update fehlgeschlagen; Sicherung in %s: %w", previous, errors.Join(cause, err))
	}
	if err := controlSpherelayService("start"); err != nil {
		return fmt.Errorf("Vorherige Programmdatei wiederhergestellt, Dienststart fehlgeschlagen: %w", errors.Join(cause, err))
	}
	if err := waitAppVersion(version); err != nil {
		return fmt.Errorf("Vorherige Programmdatei wiederhergestellt, Startprüfung fehlgeschlagen: %w", errors.Join(cause, err))
	}
	return fmt.Errorf("Update zurückgerollt. Die bisherige Version läuft wieder: %w", cause)
}
