package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type hevcCameraStart struct {
	sets         [3][]byte
	h264         bool
	payloadType  byte
	pending      []string
	deadline     time.Time
	packets      [][]byte
	bytes        int
	fragment     bool
	fragmentType byte
	preroll      [][]byte
	prerollBytes int
	control      [][]byte
}

func (s *hevcCameraStart) readParameterSets(sdp string) {
	for i, name := range []string{"sprop-vps=", "sprop-sps=", "sprop-pps="} {
		_, tail, found := strings.Cut(sdp, name)
		if !found {
			continue
		}
		value, _, _ := strings.Cut(tail, ";")
		value, _, _ = strings.Cut(value, "\r")
		p, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
		if err == nil && len(p) >= 2 && len(p) <= 1400 && (p[0]>>1)&63 == byte(32+i) {
			s.sets[i] = p
		}
	}
}

func (s *hevcCameraStart) readSDP(sdp string) {
	*s = hevcCameraStart{}
	for _, line := range strings.Split(sdp, "\n") {
		mapping, ok := strings.CutPrefix(strings.TrimSpace(line), "a=rtpmap:")
		payload, codec, found := strings.Cut(mapping, " ")
		if !ok || !found {
			continue
		}
		if !strings.EqualFold(codec, "H264/90000") && !strings.EqualFold(codec, "H265/90000") {
			continue
		}
		id, err := strconv.ParseUint(payload, 10, 7)
		if err != nil {
			continue
		}
		s.payloadType = byte(id)
		s.h264 = strings.EqualFold(codec, "H264/90000")
		for _, candidate := range strings.Split(sdp, "\n") {
			params, ok := strings.CutPrefix(strings.TrimSpace(candidate), "a=fmtp:"+payload+" ")
			if !ok {
				continue
			}
			if !s.h264 {
				s.readParameterSets(params)
				return
			}
			_, value, found := strings.Cut(params, "sprop-parameter-sets=")
			if found {
				value, _, _ = strings.Cut(value, ";")
				for _, entry := range strings.Split(value, ",") {
					nal, err := base64.StdEncoding.DecodeString(strings.TrimSpace(entry))
					if err == nil && len(nal) > 1 && len(nal) <= 1400 {
						switch nal[0] & 31 {
						case 7:
							s.sets[0] = nal
						case 8:
							s.sets[1] = nal
						}
					}
				}
			}
			return
		}
		return
	}
}

func (s *hevcCameraStart) ready() bool {
	return s.sets[0] != nil && s.sets[1] != nil && (s.h264 || s.sets[2] != nil)
}

func (s *hevcCameraStart) resetFrame() {
	s.packets, s.bytes, s.fragment = nil, 0, false
}

func (s *hevcCameraStart) forwardPending(dst io.Writer) error {
	reply := "RTSP/1.0 200 OK\r\n" + strings.Join(s.pending, "\r\n") + "\r\n\r\n"
	if err := writeHEVCStart(dst, []byte(reply)); err != nil {
		return err
	}
	for _, packet := range s.preroll {
		if err := writeHEVCStart(dst, packet); err != nil {
			return err
		}
	}
	s.clearPending()
	return nil
}

func (s *hevcCameraStart) clearPending() {
	s.pending, s.preroll, s.control = nil, nil, nil
	s.prerollBytes = 0
	s.resetFrame()
}

func (s *hevcCameraStart) feed(dst io.Writer, packet []byte, channel byte, trace func(string)) error {
	if time.Now().After(s.deadline) || s.prerollBytes+len(packet) > 8*1024*1024 {
		if err := s.forwardPending(dst); err != nil {
			return err
		}
		return writeHEVCStart(dst, packet)
	}
	saved := append([]byte(nil), packet...)
	s.preroll = append(s.preroll, saved)
	s.prerollBytes += len(saved)
	if packet[1] != channel {
		s.control = append(s.control, saved)
		return nil
	}
	if len(packet) < 16 {
		return nil
	}
	payload := rtpVideoPayload(packet[4:])
	if len(payload) < 2 || packet[5]&127 != s.payloadType {
		return nil
	}
	kind := (payload[0] >> 1) & 63
	startsIDR := kind == 19 || kind == 20
	fragmented, fuIndex := kind == 49, 2
	if s.h264 {
		kind = payload[0] & 31
		startsIDR = beginsH264IDR(payload)
		fragmented, fuIndex = kind == 28, 1
	}
	var fu byte
	if fragmented {
		if len(payload) <= fuIndex+1 {
			s.resetFrame()
			return nil
		}
		fu = payload[fuIndex]
		nal := fu & 63
		if s.h264 {
			nal = fu & 31
		}
		startsIDR = fu&128 != 0 && ((!s.h264 && (nal == 19 || nal == 20)) || (s.h264 && nal == 5))
	}
	if len(s.packets) > 0 {
		last := s.packets[len(s.packets)-1]
		if binary.BigEndian.Uint16(last[6:])+1 != binary.BigEndian.Uint16(packet[6:]) ||
			binary.BigEndian.Uint32(last[8:]) != binary.BigEndian.Uint32(packet[8:]) ||
			binary.BigEndian.Uint32(last[12:]) != binary.BigEndian.Uint32(packet[12:]) {
			s.resetFrame()
		}
	}
	if len(s.packets) == 0 && !startsIDR {
		return nil
	}
	if fragmented {
		nalType := fu & 63
		if s.h264 {
			nalType = fu & 31
		}
		if (fu&128 != 0) == s.fragment || fu&0xc0 == 0xc0 ||
			(s.fragment && nalType != s.fragmentType) {
			s.resetFrame()
			return nil
		}
		s.fragment = fu&64 == 0
		s.fragmentType = nalType
	} else if s.fragment {
		s.resetFrame()
		return nil
	}
	if s.bytes+len(packet) > 8*1024*1024 {
		s.resetFrame()
		return nil
	}
	s.packets = append(s.packets, saved)
	s.bytes += len(packet)
	if packet[5]&128 == 0 || s.fragment {
		return nil
	}
	return s.flush(dst, trace)
}

var hevcRTPInfoNumbers = regexp.MustCompile("(?i);(?:seq|rtptime)=[0-9]+")

func hasSingleRTPInfo(headers []string) bool {
	for _, header := range headers {
		key, value, _ := strings.Cut(header, ":")
		if strings.EqualFold(key, "RTP-Info") {
			return strings.Contains(value, "url=") && !strings.Contains(value, ",")
		}
	}
	return false
}

// Measured at the camera during reconnect, from PLAY OK to VPS and
// from VPS to the RTP marker ending the first access unit.
const (
	cameraFirstVideoDelay = 45 * time.Millisecond
	cameraFirstIDRSpan    = 70 * time.Millisecond
)

func (s *hevcCameraStart) flush(dst io.Writer, trace func(string)) error {
	first := s.packets[0]
	count := len(s.sets)
	if s.h264 {
		count--
	}
	seq := binary.BigEndian.Uint16(first[6:]) - uint16(count)
	stamp := binary.BigEndian.Uint32(first[8:])
	found := false
	for i, header := range s.pending {
		key, value, _ := strings.Cut(header, ":")
		if strings.EqualFold(key, "RTP-Info") && strings.Contains(value, "url=") {
			s.pending[i] = hevcRTPInfoNumbers.ReplaceAllString(header, "") + fmt.Sprintf(";seq=%d;rtptime=%d", seq, stamp)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("RTSP-Start: RTP-Info fehlt")
	}
	reply := "RTSP/1.0 200 OK\r\n" + strings.Join(s.pending, "\r\n") + "\r\n\r\n"
	if err := writeHEVCStart(dst, []byte(reply)); err != nil {
		return err
	}
	if trace != nil {
		trace("PLAY-Antwort zum Client geschrieben: " + rtspProxyTraceMessage("RTSP/1.0 200 OK", s.pending, nil))
	}
	time.Sleep(cameraFirstVideoDelay)
	videoStart := time.Now()
	var parameters []byte
	for i, set := range s.sets[:count] {
		packet := make([]byte, 16+len(set))
		packet[0], packet[1], packet[4], packet[5] = '$', first[1], 0x80, first[5]&127
		binary.BigEndian.PutUint16(packet[2:], uint16(12+len(set)))
		binary.BigEndian.PutUint16(packet[6:], seq+uint16(i))
		copy(packet[8:16], first[8:16])
		copy(packet[16:], set)
		parameters = append(parameters, packet...)
	}
	if err := writeHEVCStart(dst, parameters); err != nil {
		return err
	}
	// Batch packets due in the same millisecond. Absolute deadlines prevent
	// timer rounding and writes from accumulating delay across the keyframe.
	for i := 0; i < len(s.packets); {
		offset := cameraIDROffset(i, len(s.packets))
		end := i + 1
		for end < len(s.packets) && cameraIDROffset(end, len(s.packets)) == offset {
			end++
		}
		if delay := time.Until(videoStart.Add(offset)); delay > 0 {
			time.Sleep(delay)
		}
		for _, packet := range s.packets[i:end] {
			if err := writeHEVCStart(dst, packet); err != nil {
				return err
			}
		}
		i = end
	}
	if trace != nil {
		trace(fmt.Sprintf("RTSP-Start: %d Parametersätze und IDR-Paketfolge bis RTP-Marker zum Client geschrieben (RTP seq=%d, rtptime=%d)", count, seq, stamp))
	}
	for _, packet := range s.control {
		if err := writeHEVCStart(dst, packet); err != nil {
			return err
		}
	}
	s.clearPending()
	return nil
}

func cameraIDROffset(index, count int) time.Duration {
	if count < 2 {
		return 0
	}
	return (cameraFirstIDRSpan * time.Duration(index) / time.Duration(count-1)).Truncate(time.Millisecond)
}

func writeHEVCStart(dst io.Writer, data []byte) error {
	n, err := dst.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
