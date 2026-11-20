package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestONVIFProfilesAndStreamURI(t *testing.T) {
	s := &Server{store: Store{Salt: "test-installation", Streams: []Stream{
		{ID: "view-one", Name: "Nord & West", Enabled: true, Width: 864, Height: 480, FPS: 30},
		{ID: "view-two", Name: "Süd", Enabled: true, Width: 640, Height: 480, FPS: 15},
		{ID: "view-disabled", Enabled: false},
	}}}
	server := httptest.NewServer(http.HandlerFunc(s.serveONVIF))
	defer server.Close()
	request := func(path, operation string) string {
		t.Helper()
		reply, err := server.Client().Post(server.URL+"/onvif/"+path, "application/soap+xml", strings.NewReader(onvifTestEnvelope("", operation)))
		if err != nil {
			t.Fatal(err)
		}
		defer reply.Body.Close()
		bytes, err := io.ReadAll(reply.Body)
		var parsed onvifEnvelope
		if err != nil || reply.StatusCode != 200 || xml.Unmarshal(bytes, &parsed) != nil {
			t.Fatalf("invalid SOAP response %d: %s", reply.StatusCode, bytes)
		}
		return string(bytes)
	}
	services := request("device_service", "<tds:GetServices/>")
	if !strings.Contains(services, "http://127.0.0.1:8899/onvif/media_service") {
		t.Fatal(services)
	}
	profiles := request("media_service", "<trt:GetProfiles/>")
	for _, expected := range []string{`token="view-one"`, `token="view-two"`, "Nord &amp; West", "<tt:Width>864</tt:Width>"} {
		if !strings.Contains(profiles, expected) {
			t.Fatalf("missing %q in %s", expected, profiles)
		}
	}
	if strings.Contains(profiles, "view-disabled") {
		t.Fatal(profiles)
	}
	uri := request("media_service", "<trt:GetStreamUri><trt:ProfileToken>view-two</trt:ProfileToken></trt:GetStreamUri>")
	if !strings.Contains(uri, "rtsp://127.0.0.1:8555/view-two") {
		t.Fatal(uri)
	}
	if s.onvifID() != (&Server{store: Store{Salt: "test-installation"}}).onvifID() {
		t.Fatal("device ID changed")
	}
}

func TestONVIFRemoteRequiresFreshDigestAndUsesLANAddress(t *testing.T) {
	const password = "viewer-test-secret"
	s := &Server{onvifLAN: true, store: Store{NetworkONVIF: true, NetworkRTSP: true, ViewerPassword: password, Streams: []Stream{{ID: "view-one", Enabled: true, Width: 640, Height: 480, FPS: 25}}}}
	op := "<trt:GetStreamUri><trt:ProfileToken>view-one</trt:ProfileToken></trt:GetStreamUri>"
	send := func(security string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://wrong-host.invalid/onvif/media_service", strings.NewReader(onvifTestEnvelope(security, op)))
		req.RemoteAddr = "192.0.2.2:10000"
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 8899}))
		reply := httptest.NewRecorder()
		s.serveONVIF(reply, req)
		return reply
	}
	if status := send("").Code; status != 401 {
		t.Fatalf("anonymous response %d", status)
	}
	created := time.Now().UTC().Format(time.RFC3339)
	nonce := "test-nonce"
	hash := sha1.Sum([]byte(nonce + created + password))
	security := "<Security><UsernameToken><Username>viewer</Username><Password>" + base64.StdEncoding.EncodeToString(hash[:]) + "</Password><Nonce>" + base64.StdEncoding.EncodeToString([]byte(nonce)) + "</Nonce><Created>" + created + "</Created></UsernameToken></Security>"
	reply := send(security)
	if reply.Code != 200 || !strings.Contains(reply.Body.String(), "rtsp://192.0.2.1:8554/view-one") || strings.Contains(reply.Body.String(), password) {
		t.Fatalf("unexpected %d %s", reply.Code, reply.Body.String())
	}
	if status := send(security).Code; status != 401 {
		t.Fatalf("replayed response %d", status)
	}
}

func onvifTestEnvelope(header, body string) string {
	return "<s:Envelope " + onvifNamespaces + "><s:Header>" + header + "</s:Header><s:Body>" + body + "</s:Body></s:Envelope>"
}

func TestDiagnosticReportHasTimestampAndRedactsCredentials(t *testing.T) {
	s := &Server{}
	s.recordDiagnostic(diagnosticEvent{Component: "rtsp", Level: "warn", Connection: "connection-one", Message: "rtsp://user:password@localhost/view?token=private"})
	reply := httptest.NewRecorder()
	s.diagnosticsAPI(reply, httptest.NewRequest("GET", "/api/diagnostics", nil))
	var report struct{ Events []diagnosticEvent }
	if err := json.Unmarshal(reply.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 1 || report.Events[0].Time.IsZero() || report.Events[0].Connection != "connection-one" {
		t.Fatal(reply.Body.String())
	}
	if strings.Contains(reply.Body.String(), "password") || strings.Contains(reply.Body.String(), "private") {
		t.Fatal(reply.Body.String())
	}
}
