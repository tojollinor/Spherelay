package main

import (
	"context"
	"encoding/xml"
	"net"
	"net/url"
	"strings"
	"time"
)

type Discovered struct {
	Address      string         `json:"address"`
	XAddr        string         `json:"xaddr"`
	RTSPAddress  string         `json:"rtspAddress,omitempty"`
	Type         string         `json:"type"`
	Method       string         `json:"method"`
	Evidence     string         `json:"evidence"`
	Category     string         `json:"category"`
	Model        string         `json:"model,omitempty"`
	Firmware     string         `json:"firmware,omitempty"`
	Manufacturer string         `json:"manufacturer,omitempty"`
	AuthStatus   string         `json:"authStatus,omitempty"`
	AuthMessage  string         `json:"authMessage,omitempty"`
	InfoPath     string         `json:"-"`
	Details      *CameraDetails `json:"details,omitempty"`
	AuthRequired bool           `json:"authRequired"`
}

func discoverONVIF(ctx context.Context) ([]Discovered, error) {
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	probe := onvifProbe()
	target, _ := net.ResolveUDPAddr("udp4", "239.255.255.250:3702")
	_, err = conn.WriteTo([]byte(probe), target)
	if err != nil {
		return nil, err
	}
	results := map[string]Discovered{}
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			items := make([]Discovered, 0, len(results))
			for _, v := range results {
				items = append(items, v)
			}
			return items, nil
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(350 * time.Millisecond))
		n, src, e := conn.ReadFrom(buf)
		if e != nil {
			if ne, ok := e.(net.Error); ok && ne.Timeout() {
				continue
			}
			return nil, e
		}
		var response struct {
			Items []struct {
				XAddrs string `xml:"XAddrs"`
				Types  string `xml:"Types"`
				Scopes string `xml:"Scopes"`
			} `xml:"Body>ProbeMatches>ProbeMatch"`
		}
		if xml.Unmarshal(buf[:n], &response) != nil {
			continue
		}
		for _, match := range response.Items {
			for _, addr := range strings.Fields(match.XAddrs) {
				u, e := url.Parse(addr)
				if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
					continue
				}
				ip := net.ParseIP(u.Hostname())
				source, _, _ := net.SplitHostPort(src.String())
				if !privateIPv4(ip) || !ip.Equal(net.ParseIP(source)) {
					continue
				}
				item := Discovered{Address: ip.String(), XAddr: addr, Type: "ONVIF-Videogerät", Method: "ONVIF", Category: "video-device", Evidence: "ONVIF-Antwort: " + match.Types}
				for _, scope := range strings.Fields(match.Scopes) {
					if model, ok := strings.CutPrefix(scope, "onvif://www.onvif.org/hardware/"); ok {
						item.Model, _ = url.PathUnescape(model)
						item.Model = strings.TrimSpace(item.Model)
						if hikvisionCameraModel.MatchString(item.Model) {
							item.Manufacturer = "Hikvision"
						}
						break
					}
				}
				results[ip.String()] = item
				break
			}
		}
	}
}

func onvifProbe() string {
	return `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl"><s:Header><a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</a:Action><a:MessageID>urn:uuid:` + discoveryUUID() + `</a:MessageID><a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To></s:Header><s:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></s:Body></s:Envelope>`
}
