package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestDescribeRTSP(t *testing.T) {
	for _, tc := range []struct {
		name, status, sdp string
		online            bool
	}{
		{"published video", "200 OK", "v=0\r\nm=video 0 RTP/AVP 96\r\n", true},
		{"missing path", "404 Not Found", "", false},
		{"audio only", "200 OK", "v=0\r\nm=audio 0 RTP/AVP 96\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const address = "rtsp://127.0.0.1:8554/view-test"
			reply := fmt.Sprintf("RTSP/1.0 %s\r\nCSeq: 1\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", tc.status, len(tc.sdp), tc.sdp)
			var request bytes.Buffer
			conn := struct {
				io.Reader
				io.Writer
			}{strings.NewReader(reply), &request}
			detail := describeRTSP(conn, address)
			if (detail == "") != tc.online {
				t.Fatalf("online = %v, detail = %q", detail == "", detail)
			}
			if !strings.HasPrefix(request.String(), "DESCRIBE "+address+" RTSP/1.0\r\n") {
				t.Fatalf("unexpected probe request %q", request.String())
			}
		})
	}
}
