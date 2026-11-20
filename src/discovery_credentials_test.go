package main

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDigestRFC7616Vectors(t *testing.T) {
	// RFC 7616 section 3.9.1: public protocol test vectors, not camera credentials.
	for _, test := range []struct{ algorithm, response string }{{"MD5", "8ca523f5e9506fed4657c9700eebdbec"}, {"SHA-256", "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"}} {
		challenge := `Digest realm="http-auth@example.org",qop="auth, auth-int",algorithm=` + test.algorithm + `,nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v",opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`
		header, err := digestAuthorization(challenge, "GET", "/dir/index.html", DiscoveryCredentials{"Mufasa", "Circle of Life"}, "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ")
		if err != nil || !strings.Contains(header, `response="`+test.response+`"`) {
			t.Fatalf("%s does not match RFC vector: %v %s", test.algorithm, err, header)
		}
	}
	for _, challenge := range []string{`Digest realm="camera",nonce="x",algorithm=unsupported`, `Digest realm="camera",nonce="x",qop="auth-int"`, "Digest realm=\"camera\",nonce=\"x\"\r\nInjected: header"} {
		if _, err := digestAuthorization(challenge, "GET", "/", DiscoveryCredentials{"demo", "test-only"}, "nonce"); err == nil {
			t.Fatal("accepted unsupported or invalid challenge")
		}
	}
}

func TestDiscoveryCredentialStorageAndNoSecretInAPI(t *testing.T) {
	s, err := newServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test"}
	s.sessions["test"] = Session{Token: "test", Expires: time.Now().Add(time.Hour)}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		if result := tRequest(t, s, method, "/api/discovery/credentials", map[string]any{}, nil); result.Code != 401 {
			t.Fatalf("%s requires login", method)
		}
	}
	password := "unit-test-camera-secret"
	result := tRequest(t, s, "POST", "/api/discovery/credentials", map[string]any{"username": "demo-user", "password": password}, cookie)
	if result.Code != 200 || strings.Contains(result.Body.String(), password) {
		t.Fatalf("save: %d %s", result.Code, result.Body.String())
	}
	disk, err := os.ReadFile(filepath.Join(s.dataDir, "discovery-credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), password) || strings.Contains(string(disk), "demo-user") {
		t.Fatal("credentials stored as plaintext")
	}
	reloaded, err := newServer(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := reloaded.readDiscoveryCredentials()
	if err != nil || saved == nil || saved.Password != password {
		t.Fatal("saved encrypted credential did not reload")
	}
	result = tRequest(t, s, "POST", "/api/discovery/credentials", map[string]any{"username": "demo-user", "password": ""}, cookie)
	if result.Code != 200 {
		t.Fatal("empty password should retain the existing secret for the same username")
	}
	result = tRequest(t, s, "POST", "/api/discovery/credentials", map[string]any{"username": "another-user", "password": ""}, cookie)
	if result.Code != 400 {
		t.Fatal("changing user must require a password")
	}
	result = tRequest(t, s, "GET", "/api/discovery/credentials", nil, cookie)
	if result.Code != 200 || strings.Contains(result.Body.String(), password) || !strings.Contains(result.Body.String(), `"hasPassword":true`) {
		t.Fatal("credential metadata response incorrect")
	}
	result = tRequest(t, s, "DELETE", "/api/discovery/credentials", map[string]any{}, cookie)
	if result.Code != 200 {
		t.Fatal("remove failed")
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "discovery-credentials.json")); !os.IsNotExist(err) {
		t.Fatal("credential file still present")
	}
}

func TestAuthenticatedCameraProbe(t *testing.T) {
	var attempts atomic.Int32
	hash := func(value string) string { sum := md5.Sum([]byte(value)); return hex.EncodeToString(sum[:]) }
	camera := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/ISAPI/System/deviceInfo" {
			t.Error("unexpected authenticated endpoint")
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("discovery must not send cookies")
		}
		auth := r.Header.Get("Authorization")
		if auth != "" {
			attempts.Add(1)
			fields, ok := parseDigest(auth)
			expected := hash(hash("demo:IP Camera(Demo):test-only-password") + ":server-nonce:" + fields["nc"] + ":" + fields["cnonce"] + ":auth:" + hash("GET:/ISAPI/System/deviceInfo"))
			if ok && fields["username"] == "demo" && fields["response"] == expected {
				fmt.Fprint(w, `<DeviceInfo><deviceType>IPCamera</deviceType><manufacturer>Example Manufacturer</manufacturer><model>ExampleCam C100</model></DeviceInfo>`)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Digest realm="IP Camera(Demo)",nonce="server-nonce",qop="auth"`)
		w.WriteHeader(401)
	}))
	defer camera.Close()
	client := authenticatedDiscoveryClient()
	defer client.CloseIdleConnections()
	candidate := Discovered{XAddr: camera.URL, Category: "camera-hint", AuthRequired: true}
	found, msg := authenticateCamera(context.Background(), client, candidate, DiscoveryCredentials{"demo", "test-only-password"})
	if found == nil || found.Model != "ExampleCam C100" || found.Manufacturer != "Example Manufacturer" || found.AuthStatus != "success" {
		t.Fatalf("authenticated info failed: %#v %s", found, msg)
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected exactly one credential attempt, got %d", attempts.Load())
	}
	attempts.Store(0)
	found, msg = authenticateCamera(context.Background(), client, candidate, DiscoveryCredentials{"demo", "wrong-test-password"})
	if found != nil || attempts.Load() != 1 || !strings.Contains(msg, "abgelehnt") {
		t.Fatal("invalid credentials retried or not reported")
	}
}

func TestAuthenticatedProbeProtectsCredentials(t *testing.T) {
	var sent atomic.Int32
	basic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sent.Add(1)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="IP Camera(Demo)"`)
		w.WriteHeader(401)
	}))
	defer basic.Close()
	client := authenticatedDiscoveryClient()
	defer client.CloseIdleConnections()
	if found, _ := authenticateCamera(context.Background(), client, Discovered{XAddr: basic.URL}, DiscoveryCredentials{"demo", "test-only"}); found != nil || sent.Load() != 0 {
		t.Fatal("Basic credentials must not be sent over plaintext HTTP")
	}
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sent.Add(1)
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="IP Camera(Demo)"`)
		w.WriteHeader(401)
	}))
	defer untrusted.Close()
	authenticateCamera(context.Background(), client, Discovered{XAddr: untrusted.URL}, DiscoveryCredentials{"demo", "test-only"})
	if sent.Load() != 0 {
		t.Fatal("credentials sent to a server with untrusted HTTPS certificate")
	}
	first, _ := http.NewRequest("GET", basic.URL, nil)
	second, _ := http.NewRequest("GET", basic.URL+"/redirect", nil)
	if client.CheckRedirect(second, []*http.Request{first}) == nil {
		t.Fatal("authenticated client follows redirects")
	}
}

func TestBasicAuthenticationWithVerifiedHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if ok && user == "demo" && password == "test-only" {
			fmt.Fprint(w, `<DeviceInfo><deviceType>IPCamera</deviceType><model>ExampleCam C100</model></DeviceInfo>`)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="IP Camera(Demo)"`)
		w.WriteHeader(401)
	}))
	defer server.Close()
	client := authenticatedDiscoveryClient()
	defer client.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
	found, _ := authenticateCamera(context.Background(), client, Discovered{XAddr: server.URL}, DiscoveryCredentials{"demo", "test-only"})
	if found == nil || found.AuthStatus != "success" {
		t.Fatal("Basic over verified HTTPS should succeed")
	}
}

func TestAuthenticationTargetsOnlyRecognizedCamerasOnce(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			attempts.Add(1)
		}
		w.Header().Set("WWW-Authenticate", `Digest realm="IP Camera(Demo)",nonce="n",qop="auth"`)
		w.WriteHeader(401)
	}))
	defer server.Close()
	devices := []Discovered{{Address: "192.168.1.1", XAddr: server.URL, Category: "camera-hint", AuthRequired: true}, {Address: "192.168.1.1", XAddr: server.URL, Category: "camera-hint", AuthRequired: true}, {Address: "192.168.1.2", XAddr: server.URL, Category: "video-device", AuthRequired: true}}
	authenticateDiscovered(context.Background(), devices, DiscoveryCredentials{"demo", "test-only"})
	if attempts.Load() != 1 {
		t.Fatalf("expected one attempt for the one recognized IP; got %d", attempts.Load())
	}
	if devices[0].AuthStatus != "failed" || devices[2].AuthStatus != "" {
		t.Fatal("authentication status or candidate filtering incorrect")
	}
}
