package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type DiscoveryNetwork struct {
	Interface string `json:"interface"`
	CIDR      string `json:"cidr"`
	Note      string `json:"note,omitempty"`
}
type WebDevice struct {
	Address string `json:"address"`
	URL     string `json:"url"`
	Status  int    `json:"status"`
}
type DiscoveryReport struct {
	Devices  []Discovered       `json:"devices"`
	Other    []WebDevice        `json:"otherWebDevices"`
	Networks []DiscoveryNetwork `json:"networks"`
	Warnings []string           `json:"warnings"`
	Checked  int32              `json:"checked"`
	Total    int                `json:"total"`
	Canceled bool               `json:"canceled"`
}

type discoveryProgress struct {
	Running bool   `json:"running"`
	Checked int32  `json:"checked"`
	Total   int    `json:"total"`
	Phase   string `json:"phase"`
}

func discoveryUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func privateIPv4(ip net.IP) bool {
	return ip != nil && ip.To4() != nil && ip.IsPrivate()
}

func discoveryNetworks() ([]DiscoveryNetwork, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	items := []DiscoveryNetwork{}
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addresses {
			ip, network, err := net.ParseCIDR(addr.String())
			if err != nil || !privateIPv4(ip) {
				continue
			}
			ones, bits := network.Mask.Size()
			if bits != 32 {
				continue
			}
			note := ""
			if ones < 24 {
				network.Mask = net.CIDRMask(24, 32)
				network.IP = ip.Mask(network.Mask)
				note = "Großes Netz: automatisch wird der /24-Bereich dieses PCs durchsucht. Weitere Bereiche können einzeln eingegeben werden."
			}
			cidr := network.String()
			if !seen[cidr] {
				items = append(items, DiscoveryNetwork{Interface: iface.Name, CIDR: cidr, Note: note})
				seen[cidr] = true
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CIDR < items[j].CIDR })
	return items, nil
}

// A scan is bounded to small private IPv4 ranges. No DNS names or arbitrary URLs.
func discoveryTargets(networks []DiscoveryNetwork) ([]string, error) {
	hosts := []string{}
	seen := map[string]bool{}
	for _, entry := range networks {
		raw := strings.TrimSpace(entry.CIDR)
		if !strings.Contains(raw, "/") {
			raw += "/32"
		}
		ip, subnet, err := net.ParseCIDR(raw)
		if err != nil || !privateIPv4(ip) {
			return nil, fmt.Errorf("Privaten IPv4-Bereich oder einzelne IP eingeben")
		}
		ones, bits := subnet.Mask.Size()
		if bits != 32 || ones < 24 {
			return nil, fmt.Errorf("Pro Bereich höchstens 256 Adressen verwenden (/24 oder kleiner)")
		}
		base := binary.BigEndian.Uint32(subnet.IP.To4())
		count := uint32(1) << uint(32-ones)
		for offset := uint32(0); offset < count; offset++ {
			if count > 2 && (offset == 0 || offset == count-1) {
				continue
			}
			v := make(net.IP, 4)
			binary.BigEndian.PutUint32(v, base+offset)
			if !privateIPv4(v) {
				return nil, fmt.Errorf("Nur private IPv4-Adressen werden durchsucht")
			}
			host := v.String()
			if !seen[host] {
				hosts = append(hosts, host)
				seen[host] = true
			}
			if len(hosts) > 1024 {
				return nil, fmt.Errorf("Höchstens 1024 Adressen pro Suche")
			}
		}
	}
	return hosts, nil
}

func discoveryClient() *http.Client {
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 800 * time.Millisecond}).DialContext,
		TLSHandshakeTimeout:    time.Second,
		ResponseHeaderTimeout:  1200 * time.Millisecond,
		MaxResponseHeaderBytes: 32 << 10,
		DisableKeepAlives:      true,
		// Discovery only: local devices commonly have self-signed certificates.
		// This client never sends credentials, cookies, or configuration writes.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &http.Client{Transport: transport, Timeout: 1800 * time.Millisecond, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		first := via[0].URL
		if len(via) >= 3 || req.URL.Hostname() != first.Hostname() || req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return http.ErrUseLastResponse
		}
		port := req.URL.Port()
		if port != "" && port != "80" && port != "443" && port != first.Port() {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

var cameraRealm = regexp.MustCompile(`(?i)realm\s*=\s*"([^"\r\n]{1,120})"`)
var cameraWords = regexp.MustCompile(`(?i)\b(ip[ -]?camera|network camera|fisheye camera)\b`)
var cameraTypeWord = regexp.MustCompile(`(?i)\bcamera\b`)
var recorderWords = regexp.MustCompile(`(?i)\b(nvr|dvr|recorder)\b`)

// A successful HTTP response or generic authentication challenge is never a camera match.
func identifyCamera(path string, status int, headers http.Header, body string) (Discovered, bool) {
	auth := status == http.StatusUnauthorized || status == http.StatusForbidden
	if path == hikvisionDevicePath && status == 200 {
		var info struct {
			XMLName      xml.Name `xml:"DeviceInfo"`
			DeviceType   string   `xml:"deviceType"`
			Model        string   `xml:"model"`
			Manufacturer string   `xml:"manufacturer"`
			Firmware     string   `xml:"firmwareVersion"`
		}
		if xml.Unmarshal([]byte(body), &info) == nil {
			identity := cameraIdentity{Manufacturer: info.Manufacturer, Model: info.Model, DeviceType: info.DeviceType, Firmware: info.Firmware}
			if identity.Manufacturer == "" && hikvisionCameraModel.MatchString(identity.Model) {
				identity.Manufacturer = "Hikvision"
			}
			return identity.discovered("HTTP-Geräteinfo", "Geräte-API meldet deviceType="+info.DeviceType)
		}
	}
	if path == axisBrandPath && status == 200 {
		values := map[string]string{}
		for _, line := range strings.Split(body, "\n") {
			if key, val, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				values[strings.TrimSpace(key)] = strings.TrimSpace(val)
			}
		}
		identity := cameraIdentity{Manufacturer: values["root.Brand.Brand"], Model: values["root.Brand.ProdNbr"], DeviceType: values["root.Brand.ProdType"]}
		if canonicalManufacturer(identity.Manufacturer) == "Axis" {
			return identity.discovered("VAPIX", "Axis-Geräte-API meldet ProdType="+identity.DeviceType)
		}
	}
	if auth {
		for _, challenge := range headers.Values("WWW-Authenticate") {
			match := cameraRealm.FindStringSubmatch(challenge)
			if len(match) < 2 {
				continue
			}
			if path == axisBrandPath && strings.HasPrefix(strings.ToUpper(match[1]), "AXIS_") {
				return Discovered{Type: "Axis-Gerät", Category: "camera-hint", Manufacturer: "Axis", Method: "VAPIX", Evidence: "Axis-Anmeldebereich erkannt", AuthRequired: true}, true
			}
			if cameraWords.MatchString(match[1]) && !recorderWords.MatchString(match[1]) {
				return Discovered{Type: "IP-Kamera (HTTP-Selbstauskunft)", Category: "camera-hint", Method: "HTTP-Anmeldebereich", Evidence: "Gerät meldet den Anmeldebereich „" + match[1] + "“; diese Antwort enthält kein Modell", AuthRequired: true}, true
			}
		}
	}
	return Discovered{}, false
}

func probeCameraHTTP(ctx context.Context, client *http.Client, base string) (*Discovered, *WebDevice) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, nil
	}
	var web *WebDevice
	var best *Discovered
	vendorHint := ""
	// These are read-only device information endpoints; no login attempts.
	for _, path := range []string{"/", hikvisionDevicePath, "/SDK/activateStatus", axisBrandPath} {
		req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Spherelay-Discovery/0.1")
		resp, err := client.Do(req)
		if err != nil {
			if path == "/" {
				return nil, nil
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
		resp.Body.Close()
		if web == nil {
			web = &WebDevice{Address: parsed.Hostname(), URL: base, Status: resp.StatusCode}
		}
		if readErr != nil {
			continue
		}
		if path == "/SDK/activateStatus" && resp.StatusCode == 200 {
			var status struct {
				XMLName xml.Name `xml:"ActivateStatus"`
			}
			if xml.Unmarshal(body, &status) == nil && (status.XMLName.Space == "http://www.hikvision.com/ver10/XMLSchema" || status.XMLName.Space == "http://www.hikvision.com/ver20/XMLSchema") {
				vendorHint = "Hikvision-kompatibel"
			}
		}
		found, ok := identifyCamera(path, resp.StatusCode, resp.Header, string(body))
		if ok && path == axisBrandPath && found.AuthRequired {
			if identity, available := readAxisIdentity(ctx, client, base); available {
				// A typed VAPIX response supersedes a vendor-only login hint.
				found, ok = identity.discovered("VAPIX", "Öffentliche Axis-Geräteinformationen")
				if ok {
					found.AuthStatus = "not-needed"
				}
			}
		}
		if !ok {
			continue
		}
		found.Address = parsed.Hostname()
		found.XAddr = base
		if path == hikvisionDevicePath || path == axisBrandPath {
			found.InfoPath = path
		}
		found.AuthRequired = found.AuthRequired || found.AuthStatus != "not-needed" && (resp.StatusCode == 401 || resp.StatusCode == 403)
		if best == nil || discoveryRank(found.Category) > discoveryRank(best.Category) {
			copy := found
			best = &copy
		} else if best.InfoPath == "" && found.InfoPath != "" {
			best.InfoPath = found.InfoPath
		}
		if found.Category == "camera" {
			break
		}
	}
	if best != nil && best.Manufacturer == "" && vendorHint != "" {
		best.Manufacturer = vendorHint
		best.Evidence += "; öffentliche XML-Antwort nutzt den Hikvision-Namensraum"
	}
	return best, web
}

func discoveryRank(category string) int {
	switch category {
	case "camera":
		return 3
	case "camera-hint":
		return 2
	case "video-device":
		return 1
	}
	return 0
}

func scanCameraNetworks(ctx context.Context, networks []DiscoveryNetwork, ports []int) (DiscoveryReport, error) {
	return scanSelectedCameraNetworks(ctx, networks, ports, false)
}

func scanSelectedCameraNetworks(ctx context.Context, networks []DiscoveryNetwork, ports []int, rtsp bool) (DiscoveryReport, error) {
	return scanCameraNetworkProgress(ctx, networks, ports, rtsp, nil)
}

func scanCameraNetworkProgress(ctx context.Context, networks []DiscoveryNetwork, ports []int, rtsp bool, progress func(int32, int)) (DiscoveryReport, error) {
	hosts, err := discoveryTargets(networks)
	report := DiscoveryReport{Devices: []Discovered{}, Other: []WebDevice{}, Warnings: []string{}, Networks: networks, Total: len(hosts) * len(ports)}
	if err != nil {
		return report, err
	}
	if rtsp {
		report.Total += len(hosts)
	}
	if progress != nil {
		progress(0, report.Total)
	}
	client := discoveryClient()
	defer client.CloseIdleConnections()
	type job struct {
		host string
		port int
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	matches := map[string]Discovered{}
	webHosts := map[string]WebDevice{}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if ctx.Err() != nil {
					return
				}
				scheme := "http"
				if task.port == 443 {
					scheme = "https"
				}
				base := scheme + "://" + net.JoinHostPort(task.host, strconv.Itoa(task.port))
				var item *Discovered
				var web *WebDevice
				if task.port == 0 {
					item = probeDiscoveryRTSP(ctx, task.host)
				} else {
					item, web = probeCameraHTTP(ctx, client, base)
				}
				if ctx.Err() == nil {
					checked := atomic.AddInt32(&report.Checked, 1)
					if progress != nil {
						progress(checked, report.Total)
					}
				}
				mu.Lock()
				if web != nil {
					if _, ok := webHosts[task.host]; !ok {
						webHosts[task.host] = *web
					}
				}
				if item != nil {
					old, exists := matches[task.host]
					if exists {
						matches[task.host] = mergeDiscovered(old, *item)
					} else {
						matches[task.host] = *item
					}
				}
				mu.Unlock()
			}
		}()
	}
send:
	for _, host := range hosts {
		for _, port := range ports {
			select {
			case jobs <- job{host, port}:
			case <-ctx.Done():
				break send
			}
		}
		if rtsp {
			select {
			case jobs <- job{host, 0}:
			case <-ctx.Done():
				break send
			}
		}
	}
	close(jobs)
	wg.Wait()
	for host, item := range matches {
		report.Devices = append(report.Devices, item)
		delete(webHosts, host)
	}
	for _, item := range webHosts {
		report.Other = append(report.Other, item)
	}
	sort.Slice(report.Devices, func(i, j int) bool { return report.Devices[i].Address < report.Devices[j].Address })
	sort.Slice(report.Other, func(i, j int) bool { return report.Other[i].Address < report.Other[j].Address })
	report.Canceled = ctx.Err() != nil
	for _, network := range networks {
		if network.Note != "" {
			report.Warnings = append(report.Warnings, network.Note)
		}
	}
	return report, nil
}

func probeDiscoveryRTSP(ctx context.Context, host string) *Discovered {
	address := net.JoinHostPort(host, "554")
	url := "rtsp://" + address + "/"
	detail := probeRTSP(ctx, address, url)
	category := "video-device"
	switch detail {
	case "":
		detail = "RTSP-Videospur am Standardpfad"
	case "RTSP-Antwort: 401":
		detail = "RTSP-Dienst verlangt Anmeldung"
	default:
		return nil
	}
	return &Discovered{Address: host, XAddr: "http://" + host, RTSPAddress: url, Type: "RTSP-Videogerät", Method: "RTSP", Category: category, Evidence: detail}
}

func (s *Server) discoverCameras(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Network        string   `json:"network"`
		Ports          []int    `json:"ports"`
		UseCredentials bool     `json:"useCredentials"`
		CredentialID   string   `json:"credentialId"`
		Methods        []string `json:"methods"`
	}
	if !readReq(w, r, &input) {
		return
	}
	if input.Methods == nil {
		input.Methods = []string{"vendor", "onvif", "rtsp"}
	}
	methods := make(map[string]bool, 3)
	for _, method := range input.Methods {
		if method != "vendor" && method != "onvif" && method != "rtsp" {
			bad(w, 400, "Unbekanntes Suchverfahren")
			return
		}
		methods[method] = true
	}
	if len(methods) == 0 {
		bad(w, 400, "Mindestens ein Suchverfahren auswählen")
		return
	}
	networks, err := discoveryNetworks()
	if input.Network != "" {
		networks = nil
		for _, area := range strings.Split(input.Network, ",") {
			networks = append(networks, DiscoveryNetwork{Interface: "Manuell", CIDR: strings.TrimSpace(area)})
		}
		err = nil
	}
	if err != nil {
		bad(w, 500, "Lokale Netzbereiche konnten nicht ermittelt werden")
		return
	}
	if len(networks) == 0 {
		bad(w, 400, "Kein privates IPv4-Netz gefunden. Bitte einen Netzwerkbereich eingeben.")
		return
	}
	if len(input.Ports) == 0 {
		input.Ports = []int{80, 443}
	}
	if len(input.Ports) > 4 {
		bad(w, 400, "Höchstens vier HTTP-Ports")
		return
	}
	for _, port := range input.Ports {
		if port < 1 || port > 65535 {
			bad(w, 400, "Ungültiger HTTP-Port")
			return
		}
	}
	hosts, err := discoveryTargets(networks)
	if err != nil {
		bad(w, 400, err.Error())
		return
	}
	scope := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		scope[host] = true
	}
	var credentials *DiscoveryCredentials
	var selectedProfileID string
	if input.UseCredentials {
		s.mu.Lock()
		var profile *CredentialProfile
		profile, err = s.selectCredentialProfile(input.CredentialID)
		if profile != nil {
			credentials = &profile.DiscoveryCredentials
			selectedProfileID = profile.ID
		}
		s.mu.Unlock()
		if err != nil {
			bad(w, 500, err.Error())
			return
		}
		if credentials == nil {
			bad(w, 400, "Zuerst Kamerazugangsdaten für die Suche speichern")
			return
		}
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.mu.Lock()
	if s.discovering {
		s.mu.Unlock()
		bad(w, 409, "Eine Kamerasuche läuft bereits")
		return
	}
	s.discovering, s.cancelDiscovery = true, cancel
	s.discoveryProgress = discoveryProgress{Running: true, Phase: "Geräte werden gesucht"}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.discovering, s.cancelDiscovery = false, nil
		s.discoveryProgress.Running = false
		s.mu.Unlock()
	}()
	type onvifResult struct {
		devices []Discovered
		err     error
	}
	onvif := make(chan onvifResult, 1)
	sadp := make(chan onvifResult, 1)
	go func() {
		if !methods["onvif"] {
			onvif <- onvifResult{}
			return
		}
		c, stop := context.WithTimeout(ctx, 4*time.Second)
		items, err := discoverONVIF(c)
		stop()
		scoped := items[:0]
		for _, item := range items {
			if scope[item.Address] {
				scoped = append(scoped, item)
			}
		}
		metadata, finish := context.WithTimeout(ctx, 2*time.Second)
		defer finish()
		enrichONVIFIdentities(metadata, scoped)
		onvif <- onvifResult{scoped, err}
	}()
	go func() {
		if !methods["vendor"] {
			sadp <- onvifResult{}
			return
		}
		c, stop := context.WithTimeout(ctx, 4*time.Second)
		defer stop()
		items, err := discoverSADP(c, networks)
		sadp <- onvifResult{items, err}
	}()
	ports := input.Ports
	if !methods["vendor"] {
		ports = nil
	}
	report, err := scanCameraNetworkProgress(ctx, networks, ports, methods["rtsp"], func(checked int32, total int) {
		s.mu.Lock()
		s.discoveryProgress.Checked = max(s.discoveryProgress.Checked, checked)
		s.discoveryProgress.Total = total
		s.mu.Unlock()
	})
	if err != nil {
		bad(w, 400, err.Error())
		return
	}
	found := <-onvif
	manufacturerResult := <-sadp
	found.devices = append(found.devices, manufacturerResult.devices...)
	if manufacturerResult.err != nil {
		report.Warnings = append(report.Warnings, manufacturerResult.err.Error()+"; HTTP-Suche wurde trotzdem ausgeführt")
	}
	if found.err != nil {
		report.Warnings = append(report.Warnings, "ONVIF-Suche nicht verfügbar: "+found.err.Error())
	}
	known := map[string]int{}
	for index, item := range report.Devices {
		known[item.Address] = index
	}
	for _, item := range found.devices {
		if index, ok := known[item.Address]; !ok {
			report.Devices = append(report.Devices, item)
			known[item.Address] = len(report.Devices) - 1
		} else {
			report.Devices[index] = mergeDiscovered(report.Devices[index], item)
		}
	}
	others := []WebDevice{}
	for _, item := range report.Other {
		if _, ok := known[item.Address]; !ok {
			others = append(others, item)
		}
	}
	if credentials != nil && methods["vendor"] {
		s.mu.Lock()
		s.discoveryProgress.Phase = "Geräteinformationen werden ergänzt"
		s.mu.Unlock()
		authenticateDiscovered(ctx, report.Devices, *credentials)
		for index := range report.Devices {
			device := &report.Devices[index]
			if ctx.Err() != nil || !methods["rtsp"] {
				break
			}
			if device.AuthStatus == "success" || device.AuthStatus == "not-needed" {
				detailCtx, stop := context.WithTimeout(ctx, 12*time.Second)
				details, e := inspectCamera(detailCtx, device.XAddr, credentials, selectedProfileID)
				stop()
				if e == nil {
					s.cacheInspection(&details)
					device.Details = &details
				} else {
					device.AuthMessage += "; weitere Informationen: " + e.Error()
				}
			}
		}
	}
	report.Canceled = ctx.Err() != nil
	report.Other = others
	sort.Slice(report.Devices, func(i, j int) bool { return report.Devices[i].Address < report.Devices[j].Address })
	s.log(fmt.Sprintf("Kamerasuche: %d Geräte mit Merkmalen, %d Webgeräte ohne Kameranachweis; %d/%d HTTP-Ziele geprüft", len(report.Devices), len(report.Other), report.Checked, report.Total))
	writeJSON(w, 200, report)
}

func (s *Server) discoveryProgressAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	progress, cancel := s.discoveryProgress, s.cancelDiscovery
	s.mu.Unlock()
	if r.Method == http.MethodPost && cancel != nil {
		cancel()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, progress)
}
