package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestHEVCCameraStartSendsCompleteIDRAndMatchingRTPInfo(t *testing.T) {
	s := hevcCameraStart{
		payloadType: 96,
		sets:        [3][]byte{{64, 1, 1}, {66, 1, 2}, {68, 1, 3}},
		pending:     []string{"CSeq: 3", "RTP-Info: url=" + cameraSDPTestURL + "/trackID=1;seq=50;rtptime=100"},
		deadline:    time.Now().Add(time.Second),
	}
	packet := func(seq uint16, marker bool, payload ...byte) []byte {
		p := make([]byte, 16+len(payload))
		p[0], p[4], p[5] = '$', 0x80, 96
		if marker {
			p[5] |= 128
		}
		binary.BigEndian.PutUint16(p[2:], uint16(len(p)-4))
		binary.BigEndian.PutUint16(p[6:], seq)
		binary.BigEndian.PutUint32(p[8:], 9000)
		copy(p[16:], payload)
		return p
	}
	var out bytes.Buffer
	for _, p := range [][]byte{packet(99, true, 2, 1, 1), packet(100, false, 98, 1, 128|19, 1)} {
		if err := s.feed(&out, p, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() != 0 {
		t.Fatal("sent before receiving a complete keyframe")
	}
	if err := s.feed(&out, packet(101, true, 98, 1, 64|19, 2), 0, nil); err != nil {
		t.Fatal(err)
	}
	head, body, _ := strings.Cut(out.String(), "\r\n\r\n")
	if !strings.Contains(head, "seq=97;rtptime=9000") {
		t.Fatal(head)
	}
	b := []byte(body)
	for i, kind := range []byte{32, 33, 34, 49, 49} {
		if len(b) < 17 || binary.BigEndian.Uint16(b[6:]) != uint16(97+i) || (b[16]>>1)&63 != kind {
			t.Fatalf("packet %d invalid", i)
		}
		b = b[4+int(binary.BigEndian.Uint16(b[2:])):]
	}
	if len(b) != 0 || s.pending != nil {
		t.Fatal("unexpected data after complete IDR")
	}
}

func TestH264CameraStartUsesStreamParameters(t *testing.T) {
	var s hevcCameraStart
	s.readSDP("v=0\r\na=rtpmap:102 H264/90000\r\na=fmtp:102 packetization-mode=1; sprop-parameter-sets=Z2QAHw==,aO8CPLA=\r\n")
	if !s.ready() || s.payloadType != 102 {
		t.Fatal("H.264 start parameters missing")
	}
	s.pending = []string{"CSeq: 3", "RTP-Info: url=rtsp://127.0.0.1:8555/view-any/trackID=0;seq=10;rtptime=10"}
	s.deadline = time.Now().Add(time.Second)
	var out bytes.Buffer
	packet := []byte{'$', 2, 0, 14, 0x80, 0x80 | 102, 0, 10, 0, 0, 0, 11, 0, 0, 0, 1, 0x65, 1}
	if err := s.feed(&out, packet, 2, nil); err != nil {
		t.Fatal(err)
	}
	head, body, _ := strings.Cut(out.String(), "\r\n\r\n")
	if !strings.Contains(head, "/view-any/trackID=0;seq=8;rtptime=11") {
		t.Fatal(head)
	}
	b := []byte(body)
	for i, kind := range []byte{7, 8, 5} {
		if len(b) < 17 || b[1] != 2 || b[5]&127 != 102 || binary.BigEndian.Uint16(b[6:]) != uint16(8+i) || b[16]&31 != kind {
			t.Fatalf("invalid H.264 start packet %d", i)
		}
		b = b[4+int(binary.BigEndian.Uint16(b[2:])):]
	}
	if len(b) != 0 || s.pending != nil {
		t.Fatal("unexpected data after complete IDR")
	}
}

func TestH264CameraStartAcceptsSTAPAWithIDR(t *testing.T) {
	s := hevcCameraStart{
		h264: true, payloadType: 96,
		sets:     [3][]byte{{0x67, 1}, {0x68, 2}},
		pending:  []string{"CSeq: 3", "RTP-Info: url=rtsp://127.0.0.1/view/trackID=0;seq=1;rtptime=2"},
		deadline: time.Now().Add(time.Second),
	}
	payload := []byte{0x78, 0, 2, 0x67, 1, 0, 2, 0x65, 3}
	packet := append([]byte{'$', 0, 0, byte(12 + len(payload)), 0x80, 0xe0, 0, 10, 0, 0, 0, 11, 0, 0, 0, 1}, payload...)
	var out bytes.Buffer
	if err := s.feed(&out, packet, 0, nil); err != nil {
		t.Fatal(err)
	}
	if s.pending != nil || !strings.Contains(out.String(), ";seq=8;rtptime=11") || !bytes.HasSuffix(out.Bytes(), packet) {
		t.Fatal("expected rewritten PLAY response and original STAP-A packet")
	}
}
