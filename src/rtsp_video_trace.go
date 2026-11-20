package main

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Only the client-to-server relay writes playSequence. The response relay owns
// the other fields and reports successful writes, not decoder acknowledgments.
type rtspVideoTrace struct {
	cameraSDP    atomic.Bool
	skipStart    atomic.Bool
	skipStartFor func(string) bool
	hevcStart    hevcCameraStart
	playSequence atomic.Int64
	playAt       time.Time
	videoChannel byte
	payloadType  byte
	h264         bool
	packets      int
	idr          bool
}

func rtspHeaderNumber(headers []string, name string) int {
	for _, header := range headers {
		key, value, _ := strings.Cut(header, ":")
		if strings.EqualFold(strings.TrimSpace(key), name) {
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func (v *rtspVideoTrace) observeResponse(headers []string, body []byte) {
	if seq := rtspHeaderNumber(headers, "CSeq"); seq != 0 && int64(seq) == v.playSequence.Load() && v.playAt.IsZero() {
		v.playAt = time.Now()
	}
	for _, header := range headers {
		key, value, _ := strings.Cut(header, ":")
		if !strings.EqualFold(strings.TrimSpace(key), "Transport") {
			continue
		}
		for _, param := range strings.Split(value, ";") {
			if channels, ok := strings.CutPrefix(strings.TrimSpace(param), "interleaved="); ok {
				channel, _, _ := strings.Cut(channels, "-")
				if id, err := strconv.ParseUint(channel, 10, 8); err == nil {
					v.videoChannel = byte(id)
				}
			}
		}
	}
	if len(body) == 0 {
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		mapping, ok := strings.CutPrefix(line, "a=rtpmap:")
		payload, codec, found := strings.Cut(strings.TrimSpace(mapping), " ")
		if ok && found && strings.EqualFold(strings.TrimSpace(codec), "H264/90000") {
			if id, err := strconv.ParseUint(payload, 10, 7); err == nil {
				v.payloadType, v.h264 = byte(id), true
			}
		}
	}
}

func (v *rtspVideoTrace) observePacket(channel byte, packet []byte, trace func(string)) {
	if v.playAt.IsZero() || !v.h264 || channel != v.videoChannel || len(packet) < 12 || packet[1]&0x7f != v.payloadType {
		return
	}
	payload := rtpVideoPayload(packet)
	if len(payload) == 0 {
		return
	}
	v.packets++
	if v.packets == 1 && trace != nil {
		trace(fmt.Sprintf("Erstes H.264-RTP-Paket zum Client geschrieben (%s nach PLAY 200 OK)", time.Since(v.playAt).Round(time.Millisecond)))
	}
	if !v.idr && beginsH264IDR(payload) {
		v.idr = true
		if trace != nil {
			trace(fmt.Sprintf("Erster H.264-IDR-Beginn zum Client geschrieben (%s nach PLAY 200 OK, RTP-Paket %d)", time.Since(v.playAt).Round(time.Millisecond), v.packets))
		}
	}
}

func rtpVideoPayload(packet []byte) []byte {
	if len(packet) < 12 || packet[0]>>6 != 2 {
		return nil
	}
	offset := 12 + 4*int(packet[0]&15)
	if offset > len(packet) {
		return nil
	}
	if packet[0]&0x10 != 0 {
		if len(packet)-offset < 4 {
			return nil
		}
		offset += 4 + 4*int(binary.BigEndian.Uint16(packet[offset+2:]))
	}
	end := len(packet)
	if packet[0]&0x20 != 0 {
		padding := int(packet[end-1])
		if padding == 0 {
			return nil
		}
		end -= padding
	}
	if offset >= end {
		return nil
	}
	return packet[offset:end]
}

func beginsH264IDR(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	switch payload[0] & 31 {
	case 5:
		return true
	case 24: // STAP-A
		hasIDR := false
		for payload = payload[1:]; len(payload) > 0; {
			if len(payload) < 2 {
				return false
			}
			size := int(binary.BigEndian.Uint16(payload))
			payload = payload[2:]
			if size == 0 || size > len(payload) {
				return false
			}
			hasIDR = hasIDR || payload[0]&31 == 5
			payload = payload[size:]
		}
		return hasIDR
	case 28: // FU-A
		return len(payload) > 2 && payload[1]&0x80 != 0 && payload[1]&31 == 5
	}
	return false
}
