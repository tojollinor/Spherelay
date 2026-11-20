package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestSpherelayUpdateRequiresMatchingReleaseExecutable(t *testing.T) {
	const name = "Spherelay-0.1.44-Windows-x64.exe"
	const target = appReleaseRepository + "/releases/download/v0.1.44/" + name
	valid := githubAppAsset{Name: name, URL: target, Digest: "sha256:" + strings.Repeat("a", 64), Size: 100}
	installer := valid
	installer.Name = "Spherelay-0.1.44-Windows-x64-Setup.exe"
	other := valid
	other.URL = "https://github.com/other/Spherelay/releases/download/v0.1.44/" + name
	unverified := valid
	unverified.Digest = ""
	for _, wrong := range []githubAppAsset{installer, other, unverified} {
		release, err := selectAppRelease(githubAppRelease{Tag: "v0.1.44", Assets: []githubAppAsset{wrong}})
		if err != nil || release.URL != "" {
			t.Fatalf("unexpected update for %+v: %+v %v", wrong, release, err)
		}
	}
	release, err := selectAppRelease(githubAppRelease{Tag: "v0.1.44", Assets: []githubAppAsset{installer, valid}})
	if err != nil || release.Version != "0.1.44" || release.URL != target || release.Size != valid.Size {
		t.Fatalf("valid release rejected: %+v %v", release, err)
	}
	if _, err := selectAppRelease(githubAppRelease{Tag: "v0.1.44-rc.1"}); err == nil {
		t.Fatal("prerelease tag accepted")
	}
}

func TestSpherelayUpdateRejectsTruncatedOrModifiedBinary(t *testing.T) {
	payload := []byte("test executable")
	hash := sha256.Sum256(payload)
	release := appRelease{Size: int64(len(payload)), Digest: hex.EncodeToString(hash[:])}
	var target bytes.Buffer
	if err := copyVerifiedAppUpdate(&target, bytes.NewReader(payload), release); err != nil || !bytes.Equal(payload, target.Bytes()) {
		t.Fatalf("valid payload rejected: %v", err)
	}
	for _, invalid := range [][]byte{payload[:5], append(bytes.Clone(payload), '!'), []byte("Test executable")} {
		if err := copyVerifiedAppUpdate(io.Discard, bytes.NewReader(invalid), release); err == nil {
			t.Fatalf("invalid payload accepted: %q", invalid)
		}
	}
}
