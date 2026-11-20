package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const version = "1.0.1"
const maxBody = 1 << 20

//go:embed web/*
var webAssets embed.FS

type Camera struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url,omitempty"`
	Secret     string `json:"secret,omitempty"`
	Projection string `json:"projection"`
	Enabled    bool   `json:"enabled"`
	AutoStart  bool   `json:"autoStart"`
}
type Stream struct {
	Paused              bool       `json:"paused,omitempty"`
	Go1984Compatibility bool       `json:"go1984Compatibility,omitempty"`
	MirrorHorizontal    bool       `json:"mirrorHorizontal"`
	MirrorVertical      bool       `json:"mirrorVertical"`
	ID                  string     `json:"id"`
	CameraID            string     `json:"cameraId"`
	Name                string     `json:"name"`
	Yaw                 float64    `json:"yaw"`
	Pitch               float64    `json:"pitch"`
	Roll                float64    `json:"roll"`
	OSD                 *OSDConfig `json:"osd,omitempty"`
	FOV                 float64    `json:"fov"`
	Width               int        `json:"width"`
	Height              int        `json:"height"`
	FPS                 int        `json:"fps"`
	Enabled             bool       `json:"enabled"`
}
type Store struct {
	LogLevels      componentLogLevels `json:"logLevels"`
	Salt           string             `json:"salt"`
	PasswordHash   string             `json:"passwordHash"`
	Cameras        []Camera           `json:"cameras"`
	Streams        []Stream           `json:"streams"`
	NetworkRTSP    bool               `json:"networkRtsp"`
	NetworkONVIF   bool               `json:"networkOnvif"`
	NetworkUI      bool               `json:"networkUi"`
	ViewerPassword string             `json:"viewerPassword"`
}
type Session struct {
	Token   string
	Expires time.Time
}
type Process struct {
	Streams   []Stream
	Camera    Camera
	LogLevel  string
	Done      chan struct{}
	Cmd       *exec.Cmd
	Since     time.Time
	LastError string
}
type Server struct {
	mu                    sync.Mutex
	store                 Store
	dataDir               string
	binDir                string
	sessions              map[string]Session
	processes             map[string]*Process
	media                 *Process
	started               time.Time
	cache                 map[string]Snapshot
	httpSrv               *http.Server
	stop                  context.CancelFunc
	logBuf                []string
	diagnosticEvents      []diagnosticEvent
	diagnosticSequence    uint64
	onvifLAN              bool
	onvifNonces           map[string]time.Time
	logmu                 sync.Mutex
	updating              bool
	networkRestartPending bool
	streamLastOnline      map[string]time.Time
	cameraRetries         map[string]cameraRetry
	discovering           bool
	cancelDiscovery       context.CancelFunc
	discoveryProgress     discoveryProgress
	inspections           map[string]cachedInspection
	previews              map[string]*cameraPreview
	components            map[string]*ComponentStatus
	circles               map[string]LensCircle
}
type Snapshot struct {
	At   time.Time
	Body []byte
	Err  string
}

func defaultDataDir() string {
	if env := os.Getenv("SPHERELAY_DATA_DIR"); env != "" {
		return env
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Spherelay")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".spherelay")
}
func newServer(dir string) (*Server, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	b := filepath.Join(dir, "bin")
	if err := os.MkdirAll(b, 0700); err != nil {
		return nil, err
	}
	s := &Server{dataDir: dir, binDir: b, sessions: map[string]Session{}, processes: map[string]*Process{}, cache: map[string]Snapshot{}, started: time.Now()}
	s.store.Cameras = []Camera{}
	s.store.Streams = []Stream{}
	buf, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err == nil {
		if err = json.Unmarshal(buf, &s.store); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if s.store.ViewerPassword == "" {
		s.store.ViewerPassword = token(20)
	}
	return s, nil
}
func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func hashPassword(salt, pw string) string {
	seed, _ := hex.DecodeString(salt)
	v := append([]byte{}, seed...)
	v = append(v, []byte(pw)...)
	sum := sha256.Sum256(v)
	d := sum[:]
	for i := 0; i < 150000; i++ {
		h := hmac.New(sha256.New, seed)
		h.Write(d)
		h.Write([]byte(pw))
		d = h.Sum(nil)
	}
	return hex.EncodeToString(d)
}
func (s *Server) saveLocked() error {
	data, err := json.MarshalIndent(s.store, "", "  ")
	if err != nil {
		return err
	}
	file := filepath.Join(s.dataDir, "settings.json")
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return replaceFile(tmp, file)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func bad(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
func (s *Server) log(msg string) {
	s.recordDiagnostic(diagnosticEvent{Component: "spherelay", Level: "info", Message: msg})
}
func (s *Server) recordDiagnostic(event diagnosticEvent) {
	event.Time = time.Now()
	event.Message = redactComponentLog(event.Message)
	if len(event.Message) > 4096 {
		event.Message = strings.ToValidUTF8(event.Message[:4096], "�") + " …"
	}
	line := event.Time.Format("2006-01-02 15:04:05.000 -07:00") + " " + event.Message
	legacy := event.Component != "mediamtx" && event.Component != "ffmpeg"
	if legacy {
		log.Println(line)
	}
	s.logmu.Lock()
	merged := false
	if event.Level == "warn" || event.Level == "error" || event.Level == "critical" {
		for i := len(s.diagnosticEvents) - 1; i >= max(0, len(s.diagnosticEvents)-128); i-- {
			previous := s.diagnosticEvents[i]
			if previous.Level == event.Level && previous.Component == event.Component && previous.Message == event.Message &&
				previous.Camera == event.Camera && previous.Stream == event.Stream && previous.Connection == event.Connection && previous.Status == event.Status {
				now := event.Time
				event.Time, event.LastTime, event.Count, event.ID = previous.Time, &now, max(1, previous.Count)+1, previous.ID
				s.diagnosticEvents = slices.Delete(s.diagnosticEvents, i, i+1)
				merged = true
				break
			}
		}
	}
	if !merged {
		s.diagnosticSequence++
		event.ID = s.diagnosticSequence
	}
	s.diagnosticEvents = append(s.diagnosticEvents, event)
	if len(s.diagnosticEvents) > 2048 {
		s.diagnosticEvents = s.diagnosticEvents[len(s.diagnosticEvents)-2048:]
	}
	if legacy {
		s.logBuf = append(s.logBuf, line)
		if len(s.logBuf) > 120 {
			s.logBuf = s.logBuf[len(s.logBuf)-120:]
		}
	}
	s.logmu.Unlock()
}
func (s *Server) onHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.api(w, r)
		return
	}
	if r.URL.Path == "/" {
		r.URL.Path = "/index.html"
	}
	f, err := webAssets.Open("web" + r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".html") {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), readerSeeker(f))
}
func readerSeeker(f fs.File) io.ReadSeeker {
	rs, ok := f.(io.ReadSeeker)
	if ok {
		return rs
	}
	b, _ := io.ReadAll(f)
	return strings.NewReader(string(b))
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		if r.Header.Get("X-Spherelay-Request") != "1" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			bad(w, 403, "Anfrage-Header fehlt")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, e := url.Parse(o)
			if e != nil || u.Host != r.Host {
				bad(w, 403, "Ungültige Herkunft")
				return
			}
		}
	}
	if r.URL.Path == "/api/info" && r.Method == "GET" {
		s.mu.Lock()
		setup := s.store.PasswordHash != ""
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"version": version, "setup": setup, "platform": runtime.GOOS})
		return
	}
	if r.URL.Path == "/api/setup" && r.Method == "POST" {
		if !isLoopback(r.RemoteAddr) {
			bad(w, 403, "Erstkonfiguration nur lokal")
			return
		}
		var in struct {
			Password string `json:"password"`
		}
		if !readReq(w, r, &in) {
			return
		}
		if len(in.Password) < 12 {
			bad(w, 400, "Mindestens 12 Zeichen erforderlich")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.store.PasswordHash != "" {
			bad(w, 409, "Bereits eingerichtet")
			return
		}
		s.store.Salt = hex.EncodeToString(randBytes(16))
		s.store.PasswordHash = hashPassword(s.store.Salt, in.Password)
		if err := s.saveLocked(); err != nil {
			bad(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if r.URL.Path == "/api/login" && r.Method == "POST" {
		var in struct {
			Password string `json:"password"`
		}
		if !readReq(w, r, &in) {
			return
		}
		s.mu.Lock()
		hash, salt := s.store.PasswordHash, s.store.Salt
		s.mu.Unlock()
		if hash == "" || subtle.ConstantTimeCompare([]byte(hash), []byte(hashPassword(salt, in.Password))) != 1 {
			time.Sleep(350 * time.Millisecond)
			bad(w, 401, "Passwort falsch")
			return
		}
		sid := token(32)
		s.mu.Lock()
		s.sessions[sid] = Session{Token: sid, Expires: time.Now().Add(12 * time.Hour)}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "spherelay_session", Value: sid, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if !s.authed(r) {
		bad(w, 401, "Anmeldung erforderlich")
		return
	}
	switch {
	case r.URL.Path == "/api/logout" && r.Method == "POST":
		if c, e := r.Cookie("spherelay_session"); e == nil {
			s.mu.Lock()
			delete(s.sessions, c.Value)
			s.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "spherelay_session", Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, 200, map[string]any{"ok": true})
	case r.URL.Path == "/api/state" && r.Method == "GET":
		s.mu.Lock()
		cams := make([]Camera, len(s.store.Cameras))
		copy(cams, s.store.Cameras)
		for i := range cams {
			cams[i].URL = maskURL(cams[i].URL)
			cams[i].Secret = ""
		}
		streams := append([]Stream{}, s.store.Streams...)
		nr := s.store.NetworkRTSP
		no := s.store.NetworkONVIF
		nu := s.store.NetworkUI
		vp := s.store.ViewerPassword
		procs := map[string]any{}
		for id, p := range s.processes {
			procs[id] = map[string]any{"running": true, "started": p.Since}
		}
		pending := map[string]bool{}
		for _, camera := range s.store.Cameras {
			if s.cameraRestartPendingLocked(camera) {
				pending[camera.ID] = true
			}
		}
		networkPending := s.networkRestartPending
		mediaPending := s.media != nil && s.media.LogLevel != configuredLogLevel(s.store.LogLevels.MediaMTX)
		media := s.media != nil
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"version": version, "cameras": cams, "streams": streams, "networkRtsp": nr, "networkOnvif": no, "networkUi": nu, "viewerPassword": vp, "processes": procs, "mediaRunning": media, "cameraRestartPending": pending, "networkRestartPending": networkPending, "mediaRestartPending": mediaPending, "started": s.started, "ffmpeg": fileExists(s.ffmpegPath()), "mediamtx": fileExists(s.mediaPath())})
	case r.URL.Path == "/api/cameras" && r.Method == "POST":
		s.cameraSave(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/cameras/") && r.Method == "DELETE":
		s.cameraDelete(w, r)
	case r.URL.Path == "/api/streams" && r.Method == "POST":
		s.streamSave(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/streams/") && r.Method == "DELETE":
		s.streamDelete(w, r)
	case r.URL.Path == "/api/discover" && r.Method == "POST":
		s.discoverCameras(w, r)
	case r.URL.Path == "/api/discovery/credentials" && (r.Method == "GET" || r.Method == "POST" || r.Method == "DELETE"):
		s.discoveryCredentialsAPI(w, r)
	case r.URL.Path == "/api/discovery/details" && r.Method == "POST":
		s.cameraDetailsAPI(w, r)
	case r.URL.Path == "/api/discovery/import" && r.Method == "POST":
		s.importCameraSourceAPI(w, r)
	case r.URL.Path == "/api/discovery/networks" && r.Method == "GET":
		items, err := discoveryNetworks()
		if err != nil {
			bad(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"networks": items})
	case r.URL.Path == "/api/pipeline/start" && r.Method == "POST":
		s.pipelineAction(w, r, true)
	case r.URL.Path == "/api/streams/status" && r.Method == "GET":
		s.streamStatusAPI(w, r)
	case r.URL.Path == "/api/streams/baseline" && r.Method == "POST":
		s.baselineTestAPI(w, r)
	case r.URL.Path == "/api/streams/control" && r.Method == "POST":
		s.streamControlAPI(w, r)
	case r.URL.Path == "/api/cameras/test" && r.Method == "POST":
		s.cameraConnectionTestAPI(w, r)
	case r.URL.Path == "/api/config/backup" && r.Method == "GET":
		s.configurationBackupAPI(w, r)
	case r.URL.Path == "/api/config/restore" && r.Method == "POST":
		s.configurationRestoreAPI(w, r)
	case r.URL.Path == "/api/pipeline/stop" && r.Method == "POST":
		s.pipelineAction(w, r, false)
	case r.URL.Path == "/api/pipeline/restart" && r.Method == "POST":
		s.restartPipelineAPI(w, r)
	case r.URL.Path == "/api/snapshot" && r.Method == "GET":
		s.snapshot(w, r)
	case r.URL.Path == "/api/preview/frame" && r.Method == "GET":
		s.previewFrame(w, r)
	case r.URL.Path == "/api/components" && r.Method == "POST":
		s.installComponent(w, r)
	case r.URL.Path == "/api/components" && r.Method == "GET":
		s.componentsAPI(w, r)
	case r.URL.Path == "/api/update" && r.Method == "GET":
		s.appUpdateAPI(w, r)
	case r.URL.Path == "/api/update/result" && r.Method == "GET":
		writeJSON(w, 200, map[string]any{"lastUpdate": lastAppUpdateResult()})
	case r.URL.Path == "/api/discover/progress" && (r.Method == "GET" || r.Method == "POST"):
		s.discoveryProgressAPI(w, r)
	case r.URL.Path == "/api/server/restart" && r.Method == "POST":
		s.restartMediaAPI(w, r)
	case r.URL.Path == "/api/windows" && (r.Method == "GET" || r.Method == "POST"):
		s.windowsIntegrationAPI(w, r)
	case r.URL.Path == "/api/config/network" && r.Method == "POST":
		s.networkConfig(w, r)
	case r.URL.Path == "/api/logs/components" && (r.Method == "GET" || r.Method == "POST"):
		s.componentLogsAPI(w, r)
	case r.URL.Path == "/api/logs" && r.Method == "GET":
		s.logmu.Lock()
		rows := append([]string{}, s.logBuf...)
		s.logmu.Unlock()
		writeJSON(w, 200, map[string]any{"lines": rows})
	case r.URL.Path == "/api/diagnostics" && r.Method == "GET":
		s.diagnosticsAPI(w, r)
	default:
		bad(w, 404, "Unbekannter Endpunkt")
	}
}
func readReq(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		bad(w, 400, "Ungültige Eingabe: "+err.Error())
		return false
	}
	return true
}
func (s *Server) authed(r *http.Request) bool {
	c, err := r.Cookie("spherelay_session")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[c.Value]
	if !ok || time.Now().After(sess.Expires) {
		delete(s.sessions, c.Value)
		return false
	}
	return true
}
func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func randBytes(n int) []byte      { b := make([]byte, n); _, _ = rand.Read(b); return b }
func fileExists(path string) bool { st, e := os.Stat(path); return e == nil && !st.IsDir() }
func maskURL(s string) string {
	u, e := url.Parse(s)
	if e != nil {
		return "[ungültige URL]"
	}
	u.User = nil
	return u.String()
}
func slug(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	re := regexp.MustCompile(`[^a-z0-9_-]+`)
	v = re.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	if len(v) > 40 {
		v = v[:40]
	}
	return v
}
func (s *Server) cameraSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		URL        string `json:"url"`
		Projection string `json:"projection"`
	}
	if !readReq(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 80 {
		bad(w, 400, "Kameraname erforderlich (max. 80 Zeichen)")
		return
	}
	if in.Projection != "fisheye" && in.Projection != "equirect" {
		bad(w, 400, "Projektionsart ungültig")
		return
	}
	var protected string
	if in.URL != "" {
		if !validRTSP(in.URL) {
			bad(w, 400, "Nur gültige RTSP-/RTSPS-Adressen erlaubt")
			return
		}
		var err error
		protected, err = protectSecret(in.URL)
		if err != nil {
			bad(w, 500, "Speichern der Zugangsdaten fehlgeschlagen")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := slices.Clone(s.store.Cameras)
	var c *Camera
	for i := range s.store.Cameras {
		if s.store.Cameras[i].ID == in.ID {
			c = &s.store.Cameras[i]
			break
		}
	}
	if c == nil {
		if strings.TrimSpace(in.URL) == "" {
			bad(w, 400, "RTSP-Adresse erforderlich")
			return
		}
		s.store.Cameras = append(s.store.Cameras, Camera{ID: "cam-" + token(6), Enabled: true})
		c = &s.store.Cameras[len(s.store.Cameras)-1]
	}
	if in.URL != "" {
		c.Secret = protected
		c.URL = maskURL(in.URL)
	}
	c.Name = strings.TrimSpace(in.Name)
	c.Projection = in.Projection
	if err := s.saveLocked(); err != nil {
		s.store.Cameras = previous
		bad(w, 500, err.Error())
		return
	}
	delete(s.circles, c.ID)
	s.log("Kamera konfiguriert: " + c.Name)
	if preview := s.previews[c.ID]; preview != nil {
		preview.cancel()
		delete(s.previews, c.ID)
	}
	writeJSON(w, 200, map[string]any{"id": c.ID, "restartRequired": s.cameraRestartPendingLocked(*c)})
}
func validRTSP(v string) bool {
	u, e := url.Parse(v)
	if e != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Hostname() == "" || len(v) > 2048 {
		return false
	}
	return true
}
func (s *Server) cameraDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/cameras/")
	s.mu.Lock()
	index := slices.IndexFunc(s.store.Cameras, func(camera Camera) bool { return camera.ID == id })
	if index < 0 {
		s.mu.Unlock()
		bad(w, 404, "Kamera nicht gefunden")
		return
	}
	cameras, streams := s.store.Cameras, s.store.Streams
	s.store.Cameras = slices.Delete(slices.Clone(cameras), index, index+1)
	s.store.Streams = slices.DeleteFunc(slices.Clone(streams), func(stream Stream) bool { return stream.CameraID == id })
	if err := s.saveLocked(); err != nil {
		s.store.Cameras, s.store.Streams = cameras, streams
		s.mu.Unlock()
		bad(w, 500, err.Error())
		return
	}
	if preview := s.previews[id]; preview != nil {
		preview.cancel()
		delete(s.previews, id)
	}
	s.mu.Unlock()
	s.stopPipeline(id)
	writeJSON(w, 200, map[string]any{"ok": true})
}
func validStreamSettings(st Stream) bool {
	return len(st.Name) > 0 && len(st.Name) <= 80 && st.Yaw >= -180 && st.Yaw <= 180 && st.Pitch >= -90 && st.Pitch <= 90 &&
		st.Roll >= -180 && st.Roll <= 180 && st.FOV >= 15 && st.FOV <= 160 && st.Width >= 320 && st.Width <= 3840 &&
		st.Height >= 240 && st.Height <= 2160 && st.FPS >= 1 && st.FPS <= 30 && validOSD(st)
}
func (s *Server) streamSave(w http.ResponseWriter, r *http.Request) {
	var st Stream
	if !readReq(w, r, &st) {
		return
	}
	if len(st.Name) == 0 || len(st.Name) > 80 || st.Yaw < -180 || st.Yaw > 180 || st.Pitch < -90 || st.Pitch > 90 || st.Roll < -180 || st.Roll > 180 || st.FOV < 15 || st.FOV > 160 || st.Width < 320 || st.Width > 3840 || st.Height < 240 || st.Height > 2160 || st.FPS < 1 || st.FPS > 30 {
		bad(w, 400, "Ungültige Streamparameter")
		return
	}
	if !validOSD(st) {
		bad(w, 400, "Ungültige OSD-Einstellungen")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existsCamera := false
	for _, c := range s.store.Cameras {
		if c.ID == st.CameraID {
			existsCamera = true
			if c.Projection == "fisheye" {
				st = constrainFisheye(st)
			}
		}
	}
	if !existsCamera {
		bad(w, 404, "Kamera nicht gefunden")
		return
	}
	if st.ID == "" {
		st.ID = "view-" + token(6)
	}
	if !strings.HasPrefix(st.ID, "view-") || len(st.ID) > 48 {
		bad(w, 400, "Ungültige Stream-ID")
		return
	}
	previous := slices.Clone(s.store.Streams)
	found := false
	for i := range s.store.Streams {
		if s.store.Streams[i].ID == st.ID {
			if s.store.Streams[i].CameraID != st.CameraID {
				bad(w, 409, "Stream kann nicht die Kamera wechseln")
				return
			}
			st.Paused = s.store.Streams[i].Paused
			s.store.Streams[i] = st
			found = true
			break
		}
	}
	if !found {
		s.store.Streams = append(s.store.Streams, st)
	}
	if err := s.saveLocked(); err != nil {
		s.store.Streams = previous
		bad(w, 500, err.Error())
		return
	}
	pending := false
	for _, camera := range s.store.Cameras {
		if camera.ID == st.CameraID {
			pending = s.cameraRestartPendingLocked(camera)
			break
		}
	}
	writeJSON(w, 200, map[string]any{"id": st.ID, "restartRequired": pending})
}
func (s *Server) streamDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/streams/")
	s.mu.Lock()
	previous := slices.Clone(s.store.Streams)
	cameraID := ""
	for i, c := range s.store.Streams {
		if c.ID == id {
			cameraID = c.CameraID
			s.store.Streams = append(s.store.Streams[:i], s.store.Streams[i+1:]...)
			break
		}
	}
	err := s.saveLocked()
	if err != nil {
		s.store.Streams = previous
	}
	pending := cameraID != "" && s.processes[cameraID] != nil
	s.mu.Unlock()
	if err != nil {
		bad(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "cameraId": cameraID, "restartRequired": pending})
}
func (s *Server) ffmpegPath() string {
	if v := os.Getenv("SPHERELAY_FFMPEG"); v != "" {
		return v
	}
	return filepath.Join(s.binDir, "ffmpeg"+exeExt())
}
func (s *Server) mediaPath() string {
	if v := os.Getenv("SPHERELAY_MEDIAMTX"); v != "" {
		return v
	}
	return filepath.Join(s.binDir, "mediamtx"+exeExt())
}
func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
func (s *Server) networkConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NetworkRTSP  bool `json:"networkRtsp"`
		NetworkONVIF bool `json:"networkOnvif"`
		NetworkUI    bool `json:"networkUi"`
	}
	if !readReq(w, r, &in) {
		return
	}
	if in.NetworkUI {
		bad(w, 501, "Remote-Webverwaltung benötigt TLS und wird in dieser Vorabversion bewusst nicht freigegeben")
		return
	}
	if in.NetworkONVIF && !in.NetworkRTSP {
		bad(w, 400, "ONVIF im LAN benötigt RTSP im LAN")
		return
	}
	s.mu.Lock()
	oldRTSP, oldONVIF := s.store.NetworkRTSP, s.store.NetworkONVIF
	s.store.NetworkRTSP = in.NetworkRTSP
	s.store.NetworkONVIF = in.NetworkONVIF
	err := s.saveLocked()
	if err != nil {
		s.store.NetworkRTSP, s.store.NetworkONVIF = oldRTSP, oldONVIF
	} else if oldRTSP != in.NetworkRTSP || oldONVIF != in.NetworkONVIF {
		s.networkRestartPending = true
	}
	pending := s.networkRestartPending
	s.mu.Unlock()
	if err != nil {
		bad(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restartRequired": pending})
}
func (s *Server) shutdown() {
	var done []chan struct{}
	s.stopPreviews()
	s.mu.Lock()
	for id, p := range s.processes {
		if p.Done != nil {
			done = append(done, p.Done)
		}
		if p.Cmd != nil && p.Cmd.Process != nil {
			_ = p.Cmd.Process.Kill()
		}
		delete(s.processes, id)
	}
	if s.media != nil && s.media.Cmd != nil && s.media.Cmd.Process != nil {
		if s.media.Done != nil {
			done = append(done, s.media.Done)
		}
		_ = s.media.Cmd.Process.Kill()
	}
	s.media = nil
	s.mu.Unlock()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for _, ch := range done {
		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}
func (s *Server) run(ctx context.Context) error {
	defer s.shutdown()
	s.log("Spherelay " + version + " gestartet")
	s.ensureMedia()
	go s.supervise(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.onHTTP)
	srv := &http.Server{Addr: "127.0.0.1:8787", Handler: mux, ReadHeaderTimeout: 8 * time.Second}
	s.httpSrv = srv
	go func() {
		<-ctx.Done()
		shut, can := context.WithTimeout(context.Background(), 5*time.Second)
		defer can()
		_ = srv.Shutdown(shut)
	}()
	l, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	probeCtx, cancelProbe := context.WithCancel(ctx)
	defer cancelProbe()
	go s.runRTSPPlayTest(probeCtx)
	go s.runONVIF(probeCtx)
	err = srv.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func main() {
	data := defaultDataDir()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			fmt.Println(version)
			return
		case "--help", "help":
			fmt.Println("Spherelay: --service (Windows-Dienst), --version, --data Pfad")
			return
		}
	}
	for i := 1; i < len(os.Args)-1; i++ {
		if os.Args[i] == "--data" {
			data = os.Args[i+1]
		}
	}
	if handleWindowsControl(os.Args) {
		return
	}
	if handleInstallerCommand(os.Args, data) {
		return
	}
	if handleWindowsLaunch(os.Args, data) {
		return
	}
	if err := os.MkdirAll(data, 0700); err != nil {
		log.Fatal("Datenverzeichnis: ", err)
	}
	logfile, err := os.OpenFile(filepath.Join(data, "startup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal("Startprotokoll: ", err)
	}
	defer logfile.Close()
	log.SetOutput(io.MultiWriter(os.Stderr, logfile))
	defer func() {
		if failure := recover(); failure != nil {
			log.Printf("Startabbruch: %v", failure)
			panic(failure)
		}
	}()
	s, e := newServer(data)
	if e != nil {
		log.Fatal(e)
	}
	if runWindowsService(s) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.run(ctx); err != nil {
		log.Fatal(err)
	}
}

// referenced from separate files
var _ = sort.Strings
var _ = strconv.Itoa
var _ = io.Copy
