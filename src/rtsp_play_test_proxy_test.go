package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

type rtspWriteCapture struct {
	writes []string
}

func TestRTSPPlayTestAppliesCameraStartToVirtualView(t *testing.T) {
	var video rtspVideoTrace
	var output bytes.Buffer
	relay := func(message string, response bool) {
		t.Helper()
		if err := relayRTSPMessage(&output, bufio.NewReader(strings.NewReader(message)), response, nil, &video); err != nil {
			t.Fatal(err)
		}
	}
	const address = "rtsp://127.0.0.1:8555/view-virtual"
	relay("DESCRIBE "+address+" RTSP/1.0\r\nCSeq: 1\r\n\r\n", false)
	sdp := "v=0\r\na=rtpmap:102 H264/90000\r\na=fmtp:102 sprop-parameter-sets=Z2QAHw==,aO8CPLA=\r\n"
	relay(fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp), true)
	relay("PLAY "+address+" RTSP/1.0\r\nCSeq: 3\r\n\r\n", false)
	output.Reset()
	relay("RTSP/1.0 200 OK\r\nCSeq: 3\r\nRTP-Info: url="+address+"/trackID=0;seq=1;rtptime=2\r\n\r\n", true)
	if output.Len() != 0 || video.hevcStart.pending == nil || video.cameraSDP.Load() {
		t.Fatal("virtual view should wait for an IDR using its own SDP")
	}
}

func TestRTSPPlayTestCameraSDPAndTrackMapping(t *testing.T) {
	var video rtspVideoTrace
	var output bytes.Buffer
	relay := func(message string, response bool) string {
		t.Helper()
		output.Reset()
		if err := relayRTSPMessage(&output, bufio.NewReader(strings.NewReader(message)), response, nil, &video); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	relay("DESCRIBE "+cameraSDPTestURL+" RTSP/1.0\r\nCSeq: 1\r\n\r\n", false)
	sdp := "v=0\r\na=rtpmap:96 H265/90000\r\na=fmtp:96 sprop-vps=QAEMAf//AWAAAAMAAAMAAAMAAAMAmawJ; sprop-sps=QgEBAWAAAAMAAAMAAAMAAAMAmaAB4CACHH+KrTuiS7/8AAQABbAgApMuADN/mAE=; sprop-pps=RAHAcvCcFAobJA==\r\n"
	response := relay(fmt.Sprintf("RTSP/1.0 200 OK\r\nContent-Length: %d\r\nContent-Type: application/sdp\r\n\r\n%s", len(sdp), sdp), true)
	head, body, _ := strings.Cut(response, "\r\n\r\n")
	if !strings.Contains(head, fmt.Sprintf("Content-Length: %d\r\n", len(body))) ||
		!strings.Contains(body, "a=control:"+cameraSDPTestURL+"/trackID=1\r\n") ||
		!strings.Contains(body, "a=x-dimensions:3840,2160") {
		t.Fatalf("unexpected camera SDP: %q", response)
	}
	if got := relay("SETUP "+cameraSDPTestURL+"/trackID=1 RTSP/1.0\r\nCSeq: 2\r\n\r\n", false); !strings.Contains(got, "/trackID=0 RTSP/1.0") {
		t.Fatal(got)
	}
	if got := relay("PLAY "+cameraSDPTestURL+"/trackID=1 RTSP/1.0\r\nCSeq: 3\r\n\r\n", false); !strings.HasPrefix(got, "PLAY "+cameraSDPTestURL+" RTSP/1.0") {
		t.Fatal(got)
	}
}

func TestRTSPPlayTestCountsOnlyForwardedH264AndDetectsIDR(t *testing.T) {
	var video rtspVideoTrace
	video.playSequence.Store(3)
	video.observeResponse(nil, []byte("a=rtpmap:96 H264/90000\r\n"))
	video.observeResponse([]string{"CSeq: 2", "Transport: RTP/AVP/TCP;interleaved=4-5"}, nil)
	if !video.playAt.IsZero() {
		t.Fatal("SETUP should not start PLAY timing")
	}
	video.observeResponse([]string{"CSeq: 3"}, nil)
	var output bytes.Buffer
	var logs []string
	frame := func(channel, payloadType byte, payload ...byte) string {
		return string(append([]byte{'$', channel, 0, byte(12 + len(payload)), 0x80, payloadType, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, payload...))
	}
	input := frame(5, 96, 0x65) + frame(4, 97, 0x65) + frame(4, 96, 0x61) + frame(4, 96, 0x7c, 0x85, 0xaa)
	if err := relayRTSPFrames(&output, strings.NewReader(input), true, func(entry string) {
		logs = append(logs, entry)
	}, &video); err != io.EOF {
		t.Fatal(err)
	}
	if output.String() != input || video.packets != 2 || !video.idr || len(logs) != 2 {
		t.Fatalf("unexpected RTP trace: packets=%d idr=%t logs=%q", video.packets, video.idr, logs)
	}
	if !beginsH264IDR([]byte{0x65}) || !beginsH264IDR([]byte{0x78, 0, 1, 0x67, 0, 1, 0x65}) ||
		beginsH264IDR([]byte{0x7c, 0x45, 0xaa}) {
		t.Fatal("incorrect H.264 NAL-unit detection")
	}
}

func (c *rtspWriteCapture) Write(data []byte) (int, error) {
	c.writes = append(c.writes, string(data))
	return len(data), nil
}

func TestRTSPPlayTestWritesSDPWithHeaders(t *testing.T) {
	sdp := "v=0\r\nm=video 0 RTP/AVP 96\r\n"
	message := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s", len(sdp), sdp)
	var output rtspWriteCapture
	if err := relayRTSPMessage(&output, bufio.NewReader(strings.NewReader(message)), true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(output.writes) != 1 || output.writes[0] != message {
		t.Fatalf("expected one complete RTSP write, got %q", output.writes)
	}
}

func TestRTSPPlayTestPreservesSDPAndRTP(t *testing.T) {
	sdp := "v=0\r\nt=0 0\r\nm=video 0 RTP/AVP 96\r\na=control:trackID=0\r\n"
	frame := "\x24\x00\x00\x03\x01\x02\x03"
	source := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Base: rtsp://127.0.0.1:8554/view/\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s%s", len(sdp), sdp, frame)
	var output bytes.Buffer
	var trace []string
	if err := relayRTSPFrames(&output, strings.NewReader(source), true, func(message string) {
		trace = append(trace, message)
	}, nil); err != io.EOF {
		t.Fatal(err)
	}
	want := strings.Replace(source, "127.0.0.1:8554", "127.0.0.1:8555", 1)
	if output.String() != want {
		t.Fatalf("unexpected reply: %q", output.String())
	}
	if len(trace) != 2 || !strings.Contains(trace[1], "weitergeleitet: RTSP/1.0 200 OK") ||
		!strings.Contains(trace[1], "127.0.0.1:8555") || !strings.Contains(trace[1], "a=control:trackID=0") {
		t.Fatalf("diagnostic output does not describe the forwarded SDP: %q", trace)
	}
}

func TestRTSPPlayTestRewritesOnlyTrackPlay(t *testing.T) {
	const base = "rtsp://127.0.0.1:8555/view-huJ_ydm0"
	const track = base + "/trackID=0"
	for _, test := range []struct{ method, target, want string }{
		{"PLAY", track, base},
		{"PLAY", base, base},
		{"SETUP", track, track},
		{"PLAY", base + "/trackID=1", base + "/trackID=1"},
	} {
		request := test.method + " " + test.target + " RTSP/1.0\r\nCSeq: 3\r\nSession: abc\r\n\r\n"
		var output bytes.Buffer
		if err := relayRTSPFrames(&output, strings.NewReader(request), false, nil, nil); err != io.EOF {
			t.Fatal(err)
		}
		if want := strings.Replace(request, test.target, test.want, 1); output.String() != want {
			t.Fatalf("wanted %q, got %q", want, output.String())
		}
	}
}

func TestRTSPPlayStartPreservesInterleavedFramesBeforeNextResponse(t *testing.T) {
	const play = "RTSP/1.0 200 OK\r\nCSeq: 3\r\nRTP-Info: url=rtsp://127.0.0.1:8555/view-test/trackID=0;seq=5;rtptime=9\r\n\r\n"
	const control = "RTSP/1.0 200 OK\r\nCSeq: 4\r\n\r\n"
	video := rtspVideoTrace{hevcStart: hevcCameraStart{h264: true, payloadType: 96, sets: [3][]byte{{0x67, 1}, {0x68, 2}}}}
	video.playSequence.Store(3)
	interleaved := string([]byte{'$', 1, 0, 4, 0x80, 200, 0, 0})
	source := play + interleaved + control
	var output bytes.Buffer
	if err := relayRTSPFrames(&output, strings.NewReader(source), true, nil, &video); err != io.EOF {
		t.Fatal(err)
	}
	if output.String() != source {
		t.Fatalf("unexpected response order: %q", output.String())
	}
}

func TestRTSPPlayWithBodyIsForwardedWithoutHoldingHeaders(t *testing.T) {
	const play = "RTSP/1.0 200 OK\r\nCSeq: 3\r\nRTP-Info: url=rtsp://127.0.0.1:8555/view-test/trackID=0;seq=5;rtptime=9\r\nContent-Length: 4\r\n\r\ndata"
	video := rtspVideoTrace{hevcStart: hevcCameraStart{h264: true, sets: [3][]byte{{0x67, 1}, {0x68, 2}}}}
	video.playSequence.Store(3)
	var output bytes.Buffer
	if err := relayRTSPFrames(&output, strings.NewReader(play), true, nil, &video); err != io.EOF {
		t.Fatal(err)
	}
	if output.String() != play {
		t.Fatalf("PLAY body not forwarded: %q", output.String())
	}
}

func TestRTSPGo1984CompatibilityKeepsPLAYRewriteAndBypassesStartFilter(t *testing.T) {
	const address = "rtsp://127.0.0.1:8555/view-compatible"
	server := &Server{store: Store{Streams: []Stream{{ID: "view-compatible", Go1984Compatibility: true}}}}
	video := rtspVideoTrace{
		skipStartFor: server.useTransparentRTSPStart,
		hevcStart:    hevcCameraStart{h264: true, sets: [3][]byte{{0x67, 1}, {0x68, 2}}},
	}
	var output bytes.Buffer
	request := "PLAY " + address + "/trackID=0 RTSP/1.0\r\nCSeq: 3\r\n\r\n"
	if err := relayRTSPFrames(&output, strings.NewReader(request), false, nil, &video); err != io.EOF {
		t.Fatal(err)
	}
	if output.String() != strings.Replace(request, "/trackID=0", "", 1) {
		t.Fatalf("PLAY was not rewritten: %q", output.String())
	}
	output.Reset()
	response := "RTSP/1.0 200 OK\r\nCSeq: 3\r\nRTP-Info: url=" + address + "/trackID=0;seq=5;rtptime=9\r\n\r\n"
	if err := relayRTSPFrames(&output, strings.NewReader(response), true, nil, &video); err != io.EOF {
		t.Fatal(err)
	}
	if output.String() != response || video.hevcStart.pending != nil {
		t.Fatalf("compatible view should forward PLAY immediately: %q", output.String())
	}
}
