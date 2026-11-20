//go:build windows

package main

import "testing"

func TestSpherelayUpdateProtocolAcceptsOnlyAReleaseVersion(t *testing.T) {
	if version, err := appUpdateProtocolVersion("spherelay://update/0.1.44"); err != nil || version != "0.1.44" {
		t.Fatalf("valid update rejected: %s %v", version, err)
	}
	for _, invalid := range []string{
		"spherelay://update/0.1.44?url=https://other.example",
		"spherelay://update/0.1.44#fragment",
		"spherelay://update/0.1.44/other",
		"spherelay://update/0.1.44%20--service",
		"spherelay://other@update/0.1.44",
	} {
		if _, err := appUpdateProtocolVersion(invalid); err == nil {
			t.Fatalf("invalid URI accepted: %s", invalid)
		}
	}
}
