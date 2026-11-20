package main

import (
	"context"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryProgressEndpointCancelsRunningSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{cancelDiscovery: cancel, discoveryProgress: discoveryProgress{Running: true, Checked: 4, Total: 9}}
	read := httptest.NewRecorder()
	s.discoveryProgressAPI(read, httptest.NewRequest("GET", "/api/discover/progress", nil))
	if ctx.Err() != nil || !strings.Contains(read.Body.String(), `"checked":4`) {
		t.Fatal(read.Body.String())
	}
	s.discoveryProgressAPI(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/discover/progress", nil))
	if ctx.Err() != context.Canceled {
		t.Fatal("search was not canceled")
	}
}

func TestCameraEvidenceAndFalsePositives(t *testing.T) {
	tests := []struct {
		name, path            string
		status                int
		realm, body, category string
	}{
		{"ISAPI camera", "/ISAPI/System/deviceInfo", 200, "", `<DeviceInfo xmlns="http://www.hikvision.com/ver20/XMLSchema"><deviceType>IPCamera</deviceType><model>DS-2CD0000-DEMO</model></DeviceInfo>`, "camera"},
		{"recorder is not camera", "/ISAPI/System/deviceInfo", 200, "", `<DeviceInfo><deviceType>NVR</deviceType><model>DS-7608</model></DeviceInfo>`, "video-device"},
		{"camera challenge", "/ISAPI/System/deviceInfo", 401, `Digest qop="auth", realm="IP Camera(Example)", nonce="unused"`, "", "camera-hint"},
		{"generic challenge", "/ISAPI/System/deviceInfo", 401, `Basic realm="Administration"`, "", ""},
		{"brand alone", "/", 200, "", `<title>Hikvision</title>`, ""},
		{"ordinary web server", "/", 200, "", `<title>Router</title>`, ""},
		{"camera text on website", "/", 200, "", `<p>We sell IP Cameras</p>`, ""},
		{"error echoing camera XML", "/ISAPI/System/deviceInfo", 404, "", `<DeviceInfo><deviceType>IPCamera</deviceType></DeviceInfo>`, ""},
		{"axis camera", "/axis-cgi/param.cgi?action=list&group=Brand", 200, "", "root.Brand.Brand=AXIS\nroot.Brand.ProdType=PTZ Dome Network Camera\nroot.Brand.ProdNbr=P5655-E", "camera"},
		{"axis noncamera", "/axis-cgi/param.cgi?action=list&group=Brand", 200, "", "root.Brand.Brand=AXIS\nroot.Brand.ProdType=Network Speaker", ""},
		{"recorder challenge", "/", 401, `Digest realm="IP Camera NVR"`, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.realm != "" {
				headers.Set("WWW-Authenticate", tt.realm)
			}
			got, ok := identifyCamera(tt.path, tt.status, headers, tt.body)
			if (tt.category != "") != ok || got.Category != tt.category {
				t.Fatalf("got match=%v category=%q; want %q", ok, got.Category, tt.category)
			}
			if ok && got.Evidence == "" {
				t.Fatal("match without evidence")
			}
		})
	}
}

func TestAxisPublicIdentityResolvesProtectedLegacyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("public device information must not send credentials")
		}
		switch r.URL.Path {
		case "/axis-cgi/param.cgi":
			w.Header().Set("WWW-Authenticate", `Digest realm="AXIS_TEST"`)
			w.WriteHeader(http.StatusUnauthorized)
		case axisDevicePath:
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || !strings.Contains(string(body), `"method":"getAllUnrestrictedProperties"`) {
				t.Errorf("unexpected public VAPIX request: %s %s", r.Method, body)
			}
			io.WriteString(w, `{"data":{"propertyList":{"Brand":"AXIS","ProdType":"Bullet Camera","ProdNbr":"M2036-LE","Version":"12.10.73"}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := discoveryClient()
	defer client.CloseIdleConnections()
	device, _ := probeCameraHTTP(context.Background(), client, server.URL)
	if device == nil || device.Manufacturer != "Axis" || device.Model != "M2036-LE" || device.Firmware != "12.10.73" || device.Category != "camera" || device.AuthRequired {
		t.Fatalf("unexpected Axis discovery: %#v", device)
	}
}

func TestONVIFMetadataNormalizesBrandWithoutAssumingCameraType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><d:GetDeviceInformationResponse xmlns:d="http://www.onvif.org/ver10/device/wsdl"><d:Manufacturer>Hanwha Techwin</d:Manufacturer><d:Model>Example</d:Model><d:FirmwareVersion>3.0</d:FirmwareVersion></d:GetDeviceInformationResponse></s:Body></s:Envelope>`)
	}))
	defer server.Close()
	device := Discovered{XAddr: server.URL, Category: "video-device"}
	readONVIFIdentity(context.Background(), server.Client(), &device)
	if device.Manufacturer != "Hanwha Vision" || device.Model != "Example" || device.Firmware != "3.0" || device.Category != "video-device" {
		t.Fatalf("unexpected ONVIF identity: %#v", device)
	}
}

func TestHTTPProbeRequiresEvidenceAndSendsNoCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Method != "GET" {
			t.Error("discovery must be unauthenticated and read-only")
		}
		if r.URL.Path == "/ISAPI/System/deviceInfo" {
			w.Header().Set("WWW-Authenticate", `Digest realm="IP Camera(Test)"`)
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, "<html><title>Webserver</title></html>")
	}))
	defer server.Close()
	client := discoveryClient()
	defer client.CloseIdleConnections()
	item, web := probeCameraHTTP(context.Background(), client, server.URL)
	if item == nil || item.Category != "camera-hint" || !item.AuthRequired || web == nil {
		t.Fatalf("missing camera hint: %#v %#v", item, web)
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Admin"`)
		w.WriteHeader(401)
	}))
	defer plain.Close()
	item, web = probeCameraHTTP(context.Background(), client, plain.URL)
	if item != nil || web == nil {
		t.Fatal("generic web server incorrectly treated as a camera")
	}
}

func TestDiscoveryTargetBounds(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "127.0.0.1", "192.168.0.0/16", "http://192.168.1.1/", "example.com", "::1", "192.168.1.1/33"} {
		if _, err := discoveryTargets([]DiscoveryNetwork{{CIDR: raw}}); err == nil {
			t.Errorf("accepted invalid target %q", raw)
		}
	}
	hosts, err := discoveryTargets([]DiscoveryNetwork{{CIDR: "192.168.12.99/24"}, {CIDR: "192.168.12.99"}})
	if err != nil || len(hosts) != 254 || hosts[0] != "192.168.12.1" || hosts[253] != "192.168.12.254" {
		t.Fatalf("incorrect subnet enumeration or deduplication: %d %v", len(hosts), err)
	}
	hosts, err = discoveryTargets([]DiscoveryNetwork{{CIDR: "10.1.1.8/31"}})
	if err != nil || len(hosts) != 2 {
		t.Fatal("point-to-point endpoints omitted")
	}
}

func TestDiscoveryRedirectDoesNotLeaveTarget(t *testing.T) {
	client := discoveryClient()
	defer client.CloseIdleConnections()
	first, _ := http.NewRequest("GET", "http://192.168.12.1/", nil)
	for _, raw := range []string{"http://example.com/", "http://192.168.12.2/", "http://192.168.12.1:8081/", "http://user:password@192.168.12.1/"} {
		req, _ := http.NewRequest("GET", raw, nil)
		if client.CheckRedirect(req, []*http.Request{first}) == nil {
			t.Errorf("allowed redirect %s", raw)
		}
	}
	same, _ := http.NewRequest("GET", "https://192.168.12.1/login", nil)
	if err := client.CheckRedirect(same, []*http.Request{first}); err != nil {
		t.Fatal("same-device HTTPS redirect blocked")
	}
}

func TestHTTPProbeCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	client := discoveryClient()
	defer client.CloseIdleConnections()
	probeCameraHTTP(ctx, client, server.URL)
	if time.Since(start) > time.Second {
		t.Fatal("probe ignored cancellation")
	}
}

func TestONVIFProbeNamespaceAndMessageID(t *testing.T) {
	message := onvifProbe()
	if !strings.Contains(message, `xmlns:dn="http://www.onvif.org/ver10/network/wsdl"`) {
		t.Fatal("ONVIF type prefix is undefined")
	}
	decoder := xml.NewDecoder(strings.NewReader(message))
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "MessageID" {
			var id string
			if err := decoder.DecodeElement(&id, &start); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(id, "urn:uuid:") || len(id) != 45 {
				t.Fatalf("invalid discovery UUID %q", id)
			}
		}
	}
}

func TestDiscoveryAPIAuthenticationAndValidation(t *testing.T) {
	s, err := newServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/discovery/networks", "/api/discover"} {
		method := "GET"
		if path == "/api/discover" {
			method = "POST"
		}
		if result := tRequest(t, s, method, path, map[string]any{}, nil); result.Code != 401 {
			t.Fatalf("discovery requires authentication: %d", result.Code)
		}
	}
	s.sessions["test-session"] = Session{Token: "test-session", Expires: time.Now().Add(time.Hour)}
	cookie := &http.Cookie{Name: "spherelay_session", Value: "test-session"}
	for _, body := range []map[string]any{{"network": "8.8.8.8"}, {"network": "10.0.0.0/8"}, {"network": "10.0.0.1", "ports": []int{0}}} {
		if result := tRequest(t, s, "POST", "/api/discover", body, cookie); result.Code != 400 {
			t.Fatalf("invalid scan input: %d %s", result.Code, result.Body.String())
		}
	}
	s.discovering = true
	if result := tRequest(t, s, "POST", "/api/discover", map[string]any{}, cookie); result.Code != 409 {
		t.Fatalf("concurrent scan not rejected: %d", result.Code)
	}
}

func TestPrivateIPv4(t *testing.T) {
	if !privateIPv4(net.ParseIP("192.168.1.1")) || privateIPv4(net.ParseIP("8.8.8.8")) || privateIPv4(net.ParseIP("fd00::1")) {
		t.Fatal("unexpected private IPv4 classification")
	}
}
