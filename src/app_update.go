package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const appReleaseRepository = "https://github.com/tojollinor/Spherelay"
const appReleaseAPI = "https://api.github.com/repos/tojollinor/Spherelay/releases/latest"
const appUpdateMaxSize = 64 << 20

var appReleaseVersion = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,7})\.(?:0|[1-9][0-9]{0,7})\.(?:0|[1-9][0-9]{0,7})$`)

type appRelease struct {
	Version, URL, Digest string
	Size                 int64
}

type appUpdateView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

type appUpdateResult struct {
	Version      string          `json:"version"`
	CheckedAt    time.Time       `json:"checkedAt"`
	ServiceReady bool            `json:"serviceReady"`
	Streams      []appUpdateView `json:"streams"`
}

func lastAppUpdateResult() *appUpdateResult {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	f, err := os.Open(filepath.Join(filepath.Dir(exe), "Spherelay.update.json"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var result appUpdateResult
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&result) != nil || result.Version != version {
		return nil
	}
	return &result
}

type githubAppAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type githubAppRelease struct {
	Tag        string           `json:"tag_name"`
	Draft      bool             `json:"draft"`
	Prerelease bool             `json:"prerelease"`
	Assets     []githubAppAsset `json:"assets"`
}

func selectAppRelease(meta githubAppRelease) (appRelease, error) {
	target := strings.TrimPrefix(meta.Tag, "v")
	if meta.Draft || meta.Prerelease || !appReleaseVersion.MatchString(target) {
		return appRelease{}, fmt.Errorf("GitHub bietet kein gültiges stabiles Release an")
	}
	result := appRelease{Version: target}
	filename := "Spherelay-" + target + "-Windows-x64.exe"
	for _, asset := range meta.Assets {
		if asset.Name != filename || asset.URL != appReleaseRepository+"/releases/download/"+meta.Tag+"/"+filename ||
			asset.Size <= 0 || asset.Size > appUpdateMaxSize {
			continue
		}
		digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if !ok || len(digest) != 64 {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		result.URL, result.Digest, result.Size = asset.URL, strings.ToLower(digest), asset.Size
		break
	}
	return result, nil
}

func githubUpdateURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

var appUpdateClient = &http.Client{
	Timeout: 3 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !githubUpdateURL(req.URL) {
			return fmt.Errorf("Unerwartetes GitHub-Downloadziel")
		}
		return nil
	},
}

func checkAppRelease(ctx context.Context) (appRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appReleaseAPI, nil)
	if err != nil {
		return appRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	response, err := appUpdateClient.Do(req)
	if err != nil {
		return appRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return appRelease{}, nil
	}
	if response.StatusCode != http.StatusOK {
		return appRelease{}, fmt.Errorf("GitHub-Updateprüfung fehlgeschlagen (HTTP %d)", response.StatusCode)
	}
	var meta githubAppRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&meta); err != nil {
		return appRelease{}, fmt.Errorf("GitHub-Release konnte nicht gelesen werden")
	}
	return selectAppRelease(meta)
}

func copyVerifiedAppUpdate(dst io.Writer, src io.Reader, release appRelease) error {
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, release.Size+1))
	if err != nil {
		return err
	}
	if size != release.Size || hex.EncodeToString(hash.Sum(nil)) != release.Digest {
		return fmt.Errorf("Größe oder SHA-256-Prüfsumme des Updates stimmt nicht überein")
	}
	return nil
}

func (s *Server) appUpdateAPI(w http.ResponseWriter, r *http.Request) {
	result := lastAppUpdateResult()
	release, err := checkAppRelease(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "lastUpdate": result})
		return
	}
	newer := newerVersion(release.Version, version)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{
		"installedVersion": version, "latestVersion": release.Version, "updateAvailable": newer,
		"canInstall": newer && release.URL != "" && runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && canRunWindowsAppUpdate(),
		"hasBinary":  release.URL != "", "releasesURL": appReleaseRepository + "/releases", "lastUpdate": result,
	})
}
