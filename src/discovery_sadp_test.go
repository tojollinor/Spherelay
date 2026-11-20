package main

import (
	"net"
	"strings"
	"testing"
)

func TestSADPCameraIdentityWithoutCredentials(t *testing.T) {
	packet := `<ProbeMatch><IPv4Address>192.168.1.1</IPv4Address><DeviceDescription>DS-2CD0000-DEMO</DeviceDescription><HttpPort>80</HttpPort></ProbeMatch>`
	allowed := map[string]bool{"192.168.1.1": true}
	found, ok := parseSADP([]byte(packet), net.ParseIP("192.168.1.1"), allowed)
	if !ok || found.Category != "camera" || found.Model != "DS-2CD0000-DEMO" || !strings.Contains(found.Manufacturer, "Hikvision") {
		t.Fatalf("missing unauthenticated device identity: %#v", found)
	}
	for _, p := range []string{strings.ReplaceAll(packet, "DS-2CD0000-DEMO", "DS-7600-DEMO"), strings.ReplaceAll(packet, "DS-2CD0000-DEMO", "Unknown Device")} {
		device, ok := parseSADP([]byte(p), net.ParseIP("192.168.1.1"), allowed)
		if !ok || device.Category != "video-device" {
			t.Fatal("recorder/unknown manufacturer device classified as camera")
		}
	}
	if _, ok := parseSADP([]byte(packet), net.ParseIP("192.168.1.2"), allowed); ok {
		t.Fatal("accepted mismatched source IP")
	}
	if _, ok := parseSADP([]byte(packet), net.ParseIP("192.168.1.1"), map[string]bool{}); ok {
		t.Fatal("accepted device outside selected scan range")
	}
}

func TestMergeCameraMetadata(t *testing.T) {
	hint := Discovered{Address: "192.168.1.1", Category: "camera-hint", Manufacturer: "Hikvision-kompatibel", Method: "HTTP", AuthRequired: true, InfoPath: "/ISAPI/System/deviceInfo"}
	confirmed := Discovered{Address: hint.Address, Category: "camera", Model: "DS-2CD0000-DEMO", Manufacturer: "Hikvision (Modellkennung)", Method: "SADP"}
	got := mergeDiscovered(hint, confirmed)
	if got.Category != "camera" || got.Model == "" || got.InfoPath == "" || !got.AuthRequired || !strings.Contains(got.Method, "HTTP") {
		t.Fatalf("metadata lost during merge: %#v", got)
	}
}
