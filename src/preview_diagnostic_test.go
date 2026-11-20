package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLivePreviewDecoderDiagnostic(t *testing.T) {
	if os.Getenv("SPHERELAY_DECODER_DIAGNOSTIC") != "1" {
		t.Skip("decoder diagnostic disabled")
	}
	s, err := newServer(os.Getenv("SPHERELAY_DIAGNOSTIC_DATA"))
	if err != nil || len(s.store.Cameras) == 0 {
		t.Fatal("Configured camera unavailable")
	}
	source, err := unprotectSecret(s.store.Cameras[0].Secret)
	if err != nil {
		t.Fatal("Configured camera secret unavailable")
	}
	args := previewArgs(source, s.store.Cameras[0].Projection)
	args = append(args[:len(args)-1], "-frames:v", "1", "pipe:1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.ffmpegPath(), args...)
	var output, stderr bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	err = cmd.Run()
	text := stderr.String()
	text = regexp.MustCompile(`(?i)rtsps?://[^\s]+`).ReplaceAllString(text, "[camera-url]")
	if u, e := url.Parse(source); e == nil && u.User != nil {
		password, _ := u.User.Password()
		for _, secret := range []string{u.User.Username(), password, url.QueryEscape(password), url.PathEscape(password)} {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[redacted]")
			}
		}
	}
	t.Logf("Decoder success=%v; JPEG bytes=%d; sanitized diagnostic: %s", err == nil, output.Len(), text)
}
