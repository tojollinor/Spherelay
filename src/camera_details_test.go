package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMultipleCredentialProfilesAndLegacyMigration(t *testing.T) {
	s, err := newServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(DiscoveryCredentials{"legacy-demo", "legacy-test-secret"})
	sealed, _ := protectSecret(string(old))
	disk, _ := json.Marshal(map[string]string{"secret": sealed})
	os.WriteFile(filepath.Join(s.dataDir, "discovery-credentials.json"), disk, 0600)
	profiles, err := s.readCredentialProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].ID != "legacy" {
		t.Fatal("old format did not migrate in memory")
	}
	s.sessions["test"] = Session{Token: "test", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test"}
	result := tRequest(t, s, "POST", "/api/discovery/credentials", map[string]any{"description": "Camera 02 – Lesekonto", "username": "second-demo", "password": "second-test-secret"}, cookie)
	if result.Code != 200 || strings.Contains(result.Body.String(), "second-test-secret") {
		t.Fatal("multiple credential save failed or leaked a password")
	}
	profiles, err = s.readCredentialProfiles()
	if err != nil || len(profiles) != 2 {
		t.Fatal("legacy credential was overwritten")
	}
	selected, err := s.selectCredentialProfile(profiles[1].ID)
	if err != nil || selected.Username != "second-demo" {
		t.Fatal("selected profile not used")
	}
	result = tRequest(t, s, "DELETE", "/api/discovery/credentials", map[string]any{"id": "legacy"}, cookie)
	if result.Code != 200 {
		t.Fatal("single profile removal failed")
	}
	profiles, _ = s.readCredentialProfiles()
	if len(profiles) != 1 || profiles[0].Username != "second-demo" {
		t.Fatal("removing one profile damaged another")
	}
}

func TestParseCameraStreamMetadata(t *testing.T) {
	xml := `<StreamingChannelList><StreamingChannel><id>101</id><channelName>Camera 01</channelName><enabled>true</enabled><Video><videoCodecType>H.265</videoCodecType><videoResolutionWidth>2560</videoResolutionWidth><videoResolutionHeight>1920</videoResolutionHeight><maxFrameRate>2500</maxFrameRate></Video><Audio><enabled>true</enabled></Audio></StreamingChannel><StreamingChannel><id>102</id><enabled>false</enabled></StreamingChannel><StreamingChannel><id>../invalid</id></StreamingChannel></StreamingChannelList>`
	streams, err := parseCameraSources([]byte(xml), "192.168.1.1", 8554)
	if err != nil || len(streams) != 1 || streams[0].FPS != 25 || !streams[0].Audio || streams[0].Verified || streams[0].URL != "rtsp://192.168.1.1:8554/Streaming/Channels/101" {
		t.Fatalf("metadata parsing incorrect: %#v %v", streams, err)
	}
	if _, err := parseCameraSources([]byte(`<DeviceInfo/>`), "192.168.1.1", 554); err == nil {
		t.Fatal("wrong document accepted as stream list")
	}
}

func TestRTSPDescriptionDigestAndSequence(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	protocolErrors := make(chan string, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			protocolErrors <- e.Error()
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := textproto.NewReader(bufio.NewReader(conn))
		if _, e := reader.ReadLine(); e != nil {
			protocolErrors <- e.Error()
			return
		}
		headers, e := reader.ReadMIMEHeader()
		if e != nil {
			protocolErrors <- e.Error()
			return
		}
		if headers.Get("Cseq") != "1" || headers.Get("Authorization") != "" {
			protocolErrors <- "first request must be anonymous and CSeq 1"
			return
		}
		fmt.Fprint(conn, "RTSP/1.0 401 Unauthorized\r\nWWW-Authenticate: Digest realm=\"IP Camera(Demo)\",nonce=\"test-nonce\"\r\nContent-Length: 0\r\n\r\n")
		reader.ReadLine()
		headers, e = reader.ReadMIMEHeader()
		if e != nil {
			protocolErrors <- e.Error()
			return
		}
		auth := headers.Get("Authorization")
		if headers.Get("Cseq") != "2" || !strings.HasPrefix(auth, "Digest ") || strings.Contains(auth, "algorithm=") || strings.Contains(auth, "qop=") {
			protocolErrors <- "legacy digest parameters or incremented CSeq incorrect"
			return
		}
		sdp := "v=0\r\nm=video 0 RTP/AVP 96\r\n"
		fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp)
		protocolErrors <- ""
	}()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
	}
	verified, status := verifyRTSPDescription(context.Background(), "rtsp://192.168.1.1/Streaming/Channels/101", &DiscoveryCredentials{"demo", "test-only"}, dial)
	if message := <-protocolErrors; message != "" {
		t.Fatal(message)
	}
	if !verified {
		t.Fatalf("RTSP SDP was not verified: %s", status)
	}
}
func TestImportUsesEncryptedCredentialAndConfirmedSource(t *testing.T) {
	s, _ := newServer(t.TempDir())
	s.sessions["test"] = Session{Token: "test", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test"}
	if err := s.writeCredentialProfiles([]CredentialProfile{{ID: "demo", Description: "Demo", DiscoveryCredentials: DiscoveryCredentials{"demo-user", "test-only-secret"}}}); err != nil {
		t.Fatal(err)
	}
	details := CameraDetails{Address: "192.168.1.1", CredentialID: "demo", Sources: []CameraSource{{ID: "101", URL: "rtsp://192.168.1.1:554/Streaming/Channels/101", Verified: true}}}
	s.cacheInspection(&details)
	result := tRequest(t, s, "POST", "/api/discovery/import", map[string]any{"inspectionId": details.InspectionID, "sourceId": "101", "name": "Camera 01", "projection": "fisheye"}, cookie)
	if result.Code != 200 || len(s.store.Cameras) != 1 {
		t.Fatalf("source import failed: %d %s", result.Code, result.Body.String())
	}
	camera := s.store.Cameras[0]
	plain, err := unprotectSecret(camera.Secret)
	if err != nil || !strings.Contains(plain, "demo-user:test-only-secret@") || strings.Contains(camera.URL, "test-only-secret") || camera.AutoStart {
		t.Fatal("source credentials not safely applied or stream unexpectedly auto-started")
	}
	result = tRequest(t, s, "POST", "/api/discovery/import", map[string]any{"inspectionId": details.InspectionID, "sourceId": "999", "name": "Camera 02", "projection": "fisheye"}, cookie)
	if result.Code != 400 {
		t.Fatal("unreported stream ID accepted")
	}
	for _, path := range []string{"/api/discovery/details", "/api/discovery/import"} {
		if result := tRequest(t, s, "POST", path, map[string]any{}, nil); result.Code != 401 {
			t.Fatal("camera details/import endpoint requires login")
		}
	}
}

func TestCameraDetailsRejectsExternalTargets(t *testing.T) {
	for _, base := range []string{"http://example.com", "http://8.8.8.8", "http://user:password@192.168.1.1", "file:///tmp/config", "http://192.168.1.1/path"} {
		if _, err := inspectCamera(context.Background(), base, nil, ""); err == nil {
			t.Fatalf("unsafe camera target accepted: %s", base)
		}
	}
}
