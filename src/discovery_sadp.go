package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var hikvisionCameraModel = regexp.MustCompile(`(?i)^(i?DS-2(?:CD|DE|DF|CV|TD))[A-Z0-9/-]*$`)

func parseSADP(data []byte, source net.IP, allowed map[string]bool) (Discovered, bool) {
	var response struct {
		XMLName xml.Name `xml:"ProbeMatch"`
		Address string   `xml:"IPv4Address"`
		Model   string   `xml:"DeviceDescription"`
		Port    int      `xml:"HttpPort"`
	}
	if xml.Unmarshal(data, &response) != nil {
		return Discovered{}, false
	}
	ip := net.ParseIP(strings.TrimSpace(response.Address))
	model := strings.TrimSpace(response.Model)
	if !privateIPv4(ip) || !ip.Equal(source) || !allowed[ip.String()] || model == "" || len(model) > 128 || response.Port < 1 || response.Port > 65535 {
		return Discovered{}, false
	}
	category, kind := "video-device", "SADP-Gerät"
	manufacturer := "Hikvision-kompatibel"
	if hikvisionCameraModel.MatchString(model) {
		category = "camera"
		kind = "IP-Kamera"
		manufacturer = "Hikvision (Modellkennung)"
	}
	return Discovered{Address: ip.String(), XAddr: "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(response.Port)), Type: kind, Category: category, Model: model, Manufacturer: manufacturer, Method: "SADP-Herstellersuche", Evidence: "Hersteller-Geräteantwort meldet Modell " + model + " ohne Anmeldung"}, true
}

func discoverSADP(ctx context.Context, networks []DiscoveryNetwork) ([]Discovered, error) {
	hosts, err := discoveryTargets(networks)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, host := range hosts {
		allowed[host] = true
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	devices := map[string]Discovered{}
	var firstError error
	for _, entry := range interfaces {
		if entry.Flags&net.FlagUp == 0 || entry.Flags&net.FlagMulticast == 0 || entry.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, e := entry.Addrs()
		if e != nil {
			continue
		}
		relevant := false
		for _, addr := range addresses {
			local, _, e := net.ParseCIDR(addr.String())
			if e != nil || !privateIPv4(local) {
				continue
			}
			for _, network := range networks {
				raw := network.CIDR
				if !strings.Contains(raw, "/") {
					raw += "/32"
				}
				_, subnet, e := net.ParseCIDR(raw)
				if e == nil && (subnet.Contains(local) || sameDiscoverySubnet(local, subnet.IP)) {
					relevant = true
				}
			}
		}
		if !relevant {
			continue
		}
		iface := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, e := net.ListenMulticastUDP("udp4", &iface, &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 37020})
			if e != nil {
				mu.Lock()
				if firstError == nil {
					firstError = fmt.Errorf("SADP auf %s nicht verfügbar", iface.Name)
				}
				mu.Unlock()
				return
			}
			defer conn.Close()
			inquiry := []byte(`<?xml version="1.0" encoding="utf-8"?><Probe><Uuid>` + discoveryUUID() + `</Uuid><Types>inquiry</Types></Probe>`)
			if _, e = conn.WriteToUDP(inquiry, &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 37020}); e != nil {
				mu.Lock()
				if firstError == nil {
					firstError = fmt.Errorf("SADP-Suchanfrage konnte nicht gesendet werden")
				}
				mu.Unlock()
				return
			}
			buffer := make([]byte, 64<<10)
			for ctx.Err() == nil {
				conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
				count, source, e := conn.ReadFromUDP(buffer)
				if e != nil {
					if n, ok := e.(net.Error); ok && n.Timeout() {
						continue
					}
					return
				}
				if device, ok := parseSADP(buffer[:count], source.IP, allowed); ok {
					mu.Lock()
					devices[device.Address] = device
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	items := []Discovered{}
	for _, device := range devices {
		items = append(items, device)
	}
	return items, firstError
}

func sameDiscoverySubnet(a, b net.IP) bool {
	return a.To4() != nil && b.To4() != nil && a.Mask(net.CIDRMask(24, 32)).Equal(b.Mask(net.CIDRMask(24, 32)))
}

func mergeDiscovered(a, b Discovered) Discovered {
	if discoveryRank(b.Category) > discoveryRank(a.Category) || (discoveryRank(b.Category) == discoveryRank(a.Category) && a.Model == "" && b.Model != "") {
		a, b = b, a
	}
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.RTSPAddress == "" {
		a.RTSPAddress = b.RTSPAddress
	}
	if a.Manufacturer == "" {
		a.Manufacturer = b.Manufacturer
	}
	a.Manufacturer = canonicalManufacturer(a.Manufacturer)
	if a.Firmware == "" {
		a.Firmware = b.Firmware
	}
	if a.InfoPath == "" {
		a.InfoPath = b.InfoPath
	}
	if a.AuthStatus == "" {
		a.AuthStatus = b.AuthStatus
		a.AuthMessage = b.AuthMessage
	}
	a.AuthRequired = a.AuthRequired || b.AuthRequired
	if a.AuthStatus == "success" || a.AuthStatus == "not-needed" {
		a.AuthRequired = false
	}
	if b.Method != "" && a.Method != b.Method {
		a.Method += " + " + b.Method
	}
	return a
}
