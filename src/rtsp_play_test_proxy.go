package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rtspPlayTestAddress = "127.0.0.1:8555"
const rtspPlayTestUpstream = "127.0.0.1:8554"
const rtspRelayWriteTimeout = 10 * time.Second

type rtspRelayWriter struct {
	net.Conn
}

func (w rtspRelayWriter) Write(data []byte) (int, error) {
	if err := w.SetWriteDeadline(time.Now().Add(rtspRelayWriteTimeout)); err != nil {
		return 0, err
	}
	return w.Conn.Write(data)
}

func (s *Server) runRTSPPlayTest(ctx context.Context) {
	listener, err := net.Listen("tcp4", rtspPlayTestAddress)
	if err != nil {
		s.log("RTSP-Proxy nicht verfügbar: " + err.Error())
		return
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	s.log("RTSP-Proxy lauscht auf " + rtspPlayTestAddress)
	slots := make(chan struct{}, 32)
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				s.relayRTSPPlayTest(ctx, client)
			}()
		default:
			_ = client.Close()
		}
	}
}

func (s *Server) relayRTSPPlayTest(ctx context.Context, client net.Conn) {
	defer client.Close()
	started := time.Now()
	connection := token(9)
	var metadata sync.Mutex
	stream := ""
	status := "active"
	trace := func(message string) {
		metadata.Lock()
		defer metadata.Unlock()
		if _, request, found := strings.Cut(message, "empfangen: "); found {
			fields := strings.Fields(request)
			if len(fields) >= 2 {
				if address, err := url.Parse(fields[1]); err == nil && address.Scheme == "rtsp" {
					stream = strings.SplitN(strings.TrimPrefix(address.Path, "/"), "/", 2)[0]
				}
			}
		}
		level := "debug"
		switch {
		case strings.Contains(message, "nicht erreichbar"):
			level, status = "error", "error"
		case strings.HasPrefix(message, "Verbindung geschlossen"):
			level, status = "info", "closed"
		case strings.Contains(message, "beendet:") && !strings.HasSuffix(message, ": EOF"):
			level, status = "warn", "error"
		case strings.Contains(message, "verbunden"), strings.HasPrefix(message, "RTSP-Start:"):
			level = "info"
		}
		s.recordDiagnostic(diagnosticEvent{Component: "rtsp", Level: level, Connection: connection, Status: status, Stream: stream, Message: "RTSP-Proxy [" + client.RemoteAddr().String() + "] " + message})
	}
	trace("Client verbunden")
	defer func() { trace("Verbindung geschlossen nach " + time.Since(started).Round(time.Millisecond).String()) }()
	dialer := net.Dialer{Timeout: 3 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp4", rtspPlayTestUpstream)
	if err != nil {
		trace("MediaMTX nicht erreichbar: " + err.Error())
		return
	}
	trace("MediaMTX-Verbindung " + upstream.LocalAddr().String() + " → " + upstream.RemoteAddr().String())
	defer upstream.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stop()
	done := make(chan struct{})
	video := rtspVideoTrace{skipStartFor: s.useTransparentRTSPStart}
	go func() {
		defer close(done)
		defer client.Close()
		defer upstream.Close()
		if err := relayRTSPFrames(rtspRelayWriter{upstream}, client, false, trace, &video); !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
			trace("Client → MediaMTX beendet: " + err.Error())
		}
	}()
	if err := relayRTSPFrames(rtspRelayWriter{client}, upstream, true, trace, &video); !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
		trace("MediaMTX → Client beendet: " + err.Error())
	}
	_ = client.Close()
	_ = upstream.Close()
	<-done
	if !video.playAt.IsZero() {
		trace(fmt.Sprintf("RTP-Bilanz: %d H.264-RTP-Pakete zum Client geschrieben; IDR-Beginn: %t", video.packets, video.idr))
	}
}

func (s *Server) useTransparentRTSPStart(target string) bool {
	address, err := url.Parse(target)
	if err != nil {
		return false
	}
	id := strings.Trim(address.Path, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, view := range s.store.Streams {
		if view.ID == id {
			return view.Go1984Compatibility
		}
	}
	return false
}

func relayRTSPFrames(dst io.Writer, src io.Reader, response bool, trace func(string), video *rtspVideoTrace) error {
	reader := bufio.NewReaderSize(src, 16*1024)
	var packet [4 + 65535]byte
	for {
		if response && video != nil && video.hevcStart.pending != nil {
			if conn, ok := src.(net.Conn); ok {
				_ = conn.SetReadDeadline(video.hevcStart.deadline)
			}
		}
		first, err := reader.Peek(1)
		if err != nil {
			var timeout net.Error
			if response && video != nil && video.hevcStart.pending != nil &&
				errors.As(err, &timeout) && timeout.Timeout() {
				if err := video.hevcStart.forwardPending(dst); err != nil {
					return err
				}
				if conn, ok := src.(net.Conn); ok {
					_ = conn.SetReadDeadline(time.Time{})
				}
				continue
			}
			return err
		}
		if first[0] == '$' {
			header, err := reader.Peek(4)
			if err != nil {
				return err
			}
			size := 4 + int(binary.BigEndian.Uint16(header[2:]))
			if _, err := io.ReadFull(reader, packet[:size]); err != nil {
				return err
			}
			if response && video != nil && video.hevcStart.pending != nil {
				if err := video.hevcStart.feed(dst, packet[:size], video.videoChannel, trace); err != nil {
					return err
				}
				if video.hevcStart.pending == nil {
					if conn, ok := src.(net.Conn); ok {
						_ = conn.SetReadDeadline(time.Time{})
					}
				}
				continue
			}
			n, err := dst.Write(packet[:size])
			if err != nil {
				return err
			}
			if n != size {
				return io.ErrShortWrite
			}
			if response && video != nil {
				video.observePacket(packet[1], packet[4:size], trace)
			}
			continue
		}
		if response && video != nil && video.hevcStart.pending != nil {
			// Preserve response order. Resume the original stream if a
			// subsequent control message arrives before the first IDR.
			if err := video.hevcStart.forwardPending(dst); err != nil {
				return err
			}
			if conn, ok := src.(net.Conn); ok {
				_ = conn.SetReadDeadline(time.Time{})
			}
		}
		if err := relayRTSPMessage(dst, reader, response, trace, video); err != nil {
			return err
		}
	}
}

func relayRTSPMessage(dst io.Writer, reader *bufio.Reader, response bool, trace func(string), video *rtspVideoTrace) error {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return err
	}
	start := strings.TrimRight(string(line), "\r\n")
	direction := "Client → MediaMTX"
	if response {
		direction = "MediaMTX → Client"
	}
	if trace != nil {
		trace(direction + " empfangen: " + redactComponentLog(start))
	}
	if !response {
		method, targetAndVersion, _ := strings.Cut(start, " ")
		target, version, valid := strings.Cut(targetAndVersion, " ")
		if valid && version == "RTSP/1.0" && video != nil {
			if method == "DESCRIBE" {
				video.cameraSDP.Store(strings.TrimSuffix(target, "/") == cameraSDPTestURL)
			}
			if video.cameraSDP.Load() && target == cameraSDPTestURL+"/trackID=1" {
				targetAndVersion = cameraSDPTestURL + "/trackID=0 " + version
				start = method + " " + targetAndVersion
			}
		}
		switch method {
		case "PLAY":
			target, version, ok := strings.Cut(targetAndVersion, " ")
			base, track := strings.CutSuffix(target, "/trackID=0")
			if ok && version == "RTSP/1.0" && track && strings.HasPrefix(base, "rtsp://"+rtspPlayTestAddress+"/view-") {
				start = "PLAY " + base + " " + version
				target = base
			}
			if video != nil && video.skipStartFor != nil {
				video.skipStart.Store(video.skipStartFor(target))
			}
		case "OPTIONS", "DESCRIBE", "SETUP", "PAUSE", "TEARDOWN", "GET_PARAMETER", "SET_PARAMETER":
		default:
			return fmt.Errorf("RTSP-Proxy unterstützt nur Lesesitzungen")
		}
	}
	headers := make([]string, 0, 12)
	size, lengthIndex, total := 0, -1, len(start)
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return err
		}
		total += len(line)
		if total > 64*1024 {
			return fmt.Errorf("RTSP-Header zu groß")
		}
		header := strings.TrimRight(string(line), "\r\n")
		if header == "" {
			break
		}
		key, value, ok := strings.Cut(header, ":")
		if !ok {
			return fmt.Errorf("Ungültiger RTSP-Header")
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "content-length":
			size, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil || size < 0 || size > 1024*1024 || lengthIndex >= 0 {
				return fmt.Errorf("Ungültige RTSP-Inhaltslänge")
			}
			lengthIndex = len(headers)
		case "content-base", "rtp-info":
			if response {
				header = strings.ReplaceAll(header, "rtsp://"+rtspPlayTestUpstream+"/", "rtsp://"+rtspPlayTestAddress+"/")
				if video != nil && video.cameraSDP.Load() && strings.EqualFold(key, "RTP-Info") {
					header = strings.ReplaceAll(header, cameraSDPTestURL+"/trackID=0", cameraSDPTestURL+"/trackID=1")
				}
			}
		}
		headers = append(headers, header)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		return err
	}
	if response && video != nil && lengthIndex >= 0 && strings.HasPrefix(start, "RTSP/1.0 200 ") &&
		strings.HasPrefix(string(body), "v=0") {
		if video.cameraSDP.Load() {
			if cameraSDP, ok := originalCameraSDP(string(body)); ok {
				body = []byte(cameraSDP)
				headers[lengthIndex] = "Content-Length: " + strconv.Itoa(len(body))
			} else {
				video.cameraSDP.Store(false)
			}
		}
		video.hevcStart.readSDP(string(body))
	}
	if response && video != nil && strings.HasPrefix(start, "RTSP/1.0 200 ") &&
		video.playSequence.Load() > 0 && int64(rtspHeaderNumber(headers, "CSeq")) == video.playSequence.Load() &&
		!video.skipStart.Load() && len(body) == 0 && video.hevcStart.ready() && hasSingleRTPInfo(headers) {
		video.hevcStart.pending = headers
		video.hevcStart.deadline = time.Now().Add(8 * time.Second)
		if trace != nil {
			trace("PLAY-Antwort bis IDR zurückgestellt: " + rtspProxyTraceMessage(start, headers, nil))
		}
		return nil
	}
	headerBlock := strings.Join(append([]string{start}, headers...), "\r\n") + "\r\n\r\n"
	message := make([]byte, 0, len(headerBlock)+len(body))
	message = append(message, headerBlock...)
	message = append(message, body...)
	if video != nil && !response && strings.HasPrefix(start, "PLAY ") {
		video.playSequence.Store(int64(rtspHeaderNumber(headers, "CSeq")))
	}
	n, err := dst.Write(message)
	if err != nil {
		return fmt.Errorf("RTSP-Nachricht konnte nicht weitergeleitet werden: %w", err)
	}
	if n != len(message) {
		return io.ErrShortWrite
	}
	if video != nil && response && strings.HasPrefix(start, "RTSP/1.0 200 ") {
		video.observeResponse(headers, body)
	}
	if trace != nil {
		trace(direction + " weitergeleitet: " + rtspProxyTraceMessage(start, headers, body))
	}
	return nil
}

func rtspProxyTraceMessage(start string, headers []string, body []byte) string {
	var details []string
	var sdp bool
	for _, header := range headers {
		key, value, _ := strings.Cut(header, ":")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "cseq", "content-base", "content-length", "transport", "session", "range", "rtp-info":
			details = append(details, header)
		case "content-type":
			details = append(details, header)
			kind, _, _ := strings.Cut(value, ";")
			sdp = strings.EqualFold(strings.TrimSpace(kind), "application/sdp")
		}
	}
	message := strings.Join(append([]string{start}, details...), " | ")
	if sdp && len(body) > 0 {
		message += "\n" + strings.TrimRight(string(body[:min(len(body), 4096)]), "\r\n")
	}
	return redactComponentLog(message)
}
