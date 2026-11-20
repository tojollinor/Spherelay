package main

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type CameraSource struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Codec    string  `json:"codec"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Audio    bool    `json:"audio"`
	Verified bool    `json:"verified"`
	Status   string  `json:"status"`
}
type CameraDetails struct {
	InspectionID string         `json:"inspectionId,omitempty"`
	Address      string         `json:"address"`
	Manufacturer string         `json:"manufacturer"`
	Model        string         `json:"model"`
	Firmware     string         `json:"firmware"`
	DeviceName   string         `json:"deviceName"`
	CredentialID string         `json:"credentialId,omitempty"`
	Sources      []CameraSource `json:"sources"`
	Options      []string       `json:"options"`
	Warnings     []string       `json:"warnings"`
}

// A fresh challenge is answered only at the exact requested endpoint, without redirects.
func cameraGET(ctx context.Context, client *http.Client, base, path string, creds *DiscoveryCredentials) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("Ungültige Geräteadresse")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("Geräteabfrage nicht erreichbar")
	}
	read := func(r *http.Response) ([]byte, int, error) {
		defer r.Body.Close()
		b, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		return b, r.StatusCode, e
	}
	if response.StatusCode != 401 || creds == nil {
		return read(response)
	}
	response.Body.Close()
	for _, challenge := range response.Header.Values("WWW-Authenticate") {
		if auth, e := digestAuthorization(challenge, "GET", path, *creds, token(18)); e == nil {
			request.Header.Set("Authorization", auth)
			break
		}
	}
	if request.Header.Get("Authorization") == "" {
		basic := false
		for _, c := range response.Header.Values("WWW-Authenticate") {
			if strings.HasPrefix(strings.ToLower(c), "basic ") {
				basic = true
			}
		}
		if request.URL.Scheme != "https" || !basic {
			return nil, 401, fmt.Errorf("Anmeldung nicht unterstützt; Basic benötigt geprüftes HTTPS")
		}
		request.SetBasicAuth(creds.Username, creds.Password)
	}
	response, err = client.Do(request.Clone(ctx))
	if err != nil {
		return nil, 0, fmt.Errorf("Geschützte Geräteabfrage fehlgeschlagen")
	}
	return read(response)
}

func parseCameraSources(data []byte, host string, port int) ([]CameraSource, error) {
	var list struct {
		XMLName  xml.Name `xml:"StreamingChannelList"`
		Channels []struct {
			ID      string `xml:"id"`
			Name    string `xml:"channelName"`
			Enabled *bool  `xml:"enabled"`
			Video   struct {
				Codec  string `xml:"videoCodecType"`
				Width  int    `xml:"videoResolutionWidth"`
				Height int    `xml:"videoResolutionHeight"`
				FPS    int    `xml:"maxFrameRate"`
			} `xml:"Video"`
			Audio struct {
				Enabled bool `xml:"enabled"`
			} `xml:"Audio"`
			Transport struct {
				Port int `xml:"rtspPortNo"`
			} `xml:"Transport"`
		} `xml:"StreamingChannel"`
	}
	if xml.Unmarshal(data, &list) != nil {
		return nil, fmt.Errorf("Keine lesbare Streamliste in der Geräteantwort")
	}
	result := []CameraSource{}
	for _, channel := range list.Channels {
		id, e := strconv.Atoi(channel.ID)
		if e != nil || id < 1 || id > 999999 || (channel.Enabled != nil && !*channel.Enabled) {
			continue
		}
		p := port
		if channel.Transport.Port > 0 && channel.Transport.Port <= 65535 {
			p = channel.Transport.Port
		}
		name := channel.Name
		if name == "" {
			name = "Stream " + channel.ID
		}
		if len(result) >= 16 {
			break
		}
		result = append(result, CameraSource{ID: channel.ID, Name: name, URL: "rtsp://" + net.JoinHostPort(host, strconv.Itoa(p)) + "/Streaming/Channels/" + channel.ID, Codec: channel.Video.Codec, Width: channel.Video.Width, Height: channel.Video.Height, FPS: float64(channel.Video.FPS) / 100, Audio: channel.Audio.Enabled, Status: "Geräte-API; RTSP noch nicht geprüft"})
	}
	return result, nil
}

func verifyCameraRTSP(ctx context.Context, raw string, creds *DiscoveryCredentials) (bool, string) {
	return verifyRTSPDescription(ctx, raw, creds, (&net.Dialer{Timeout: 2 * time.Second}).DialContext)
}

func verifyRTSPDescription(ctx context.Context, raw string, creds *DiscoveryCredentials, dial func(context.Context, string, string) (net.Conn, error)) (bool, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "rtsp" || u.User != nil || !privateIPv4(net.ParseIP(u.Hostname())) {
		return false, "Ungültige RTSP-Adresse"
	}
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "554")
	}
	conn, err := dial(ctx, "tcp4", address)
	if err != nil {
		return false, "RTSP-Port nicht erreichbar"
	}
	defer conn.Close()
	deadline := time.Now().Add(4 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	reader := bufio.NewReaderSize(io.LimitReader(conn, 256<<10), 32<<10)
	sequence := 0
	describe := func(auth string) (int, textproto.MIMEHeader, []byte, error) {
		sequence++
		headers := ""
		if auth != "" {
			headers = "Authorization: " + auth + "\r\n"
		}
		if _, e := fmt.Fprintf(conn, "DESCRIBE %s RTSP/1.0\r\nCSeq: %d\r\nAccept: application/sdp\r\nUser-Agent: Spherelay\r\n%s\r\n", raw, sequence, headers); e != nil {
			return 0, nil, nil, e
		}
		tp := textproto.NewReader(reader)
		line, e := tp.ReadLine()
		if e != nil {
			return 0, nil, nil, e
		}
		if len(line) > 4096 {
			return 0, nil, nil, fmt.Errorf("RTSP-Antwort zu lang")
		}
		var code int
		if _, e = fmt.Sscanf(line, "RTSP/1.0 %d", &code); e != nil {
			return 0, nil, nil, e
		}
		h, e := tp.ReadMIMEHeader()
		if e != nil {
			return 0, nil, nil, e
		}
		n, _ := strconv.Atoi(h.Get("Content-Length"))
		if n < 0 || n > 128<<10 {
			return 0, nil, nil, fmt.Errorf("RTSP-Antwort zu groß")
		}
		body := make([]byte, n)
		_, e = io.ReadFull(reader, body)
		return code, h, body, e
	}
	code, headers, body, err := describe("")
	if err != nil {
		return false, "RTSP-Beschreibung nicht lesbar"
	}
	if code == 401 && creds != nil {
		auth := ""
		for _, challenge := range headers.Values("WWW-Authenticate") {
			if a, e := digestAuthorization(challenge, "DESCRIBE", raw, *creds, token(18)); e == nil {
				auth = a
				break
			}
		}
		if auth == "" {
			return false, "RTSP-Anmeldung benötigt ein unterstütztes Digest-Verfahren"
		}
		code, headers, body, err = describe(auth)
	}
	if err != nil {
		return false, "RTSP-Beschreibung nicht lesbar"
	}
	if code == 401 || code == 403 {
		return false, "RTSP-Zugang nicht bestätigt"
	}
	if code == 200 && strings.Contains(strings.ToLower(headers.Get("Content-Type")), "application/sdp") && strings.Contains(string(body), "m=video") {
		return true, "RTSP-DESCRIBE bestätigt (Video-SDP; kein Wiedergabetest)"
	}
	return false, fmt.Sprintf("RTSP nicht bestätigt (Status %d)", code)
}

func inspectCamera(ctx context.Context, base string, creds *DiscoveryCredentials, credentialID string) (CameraDetails, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || !privateIPv4(net.ParseIP(u.Hostname())) || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return CameraDetails{}, fmt.Errorf("Private Kamera-HTTP-Adresse erforderlich")
	}
	base = strings.TrimSuffix(base, "/")
	result := CameraDetails{Address: u.Hostname(), CredentialID: credentialID, Sources: []CameraSource{}, Options: []string{}, Warnings: []string{}}
	publicClient := discoveryClient()
	axis, axisFound := readAxisIdentity(ctx, publicClient, base)
	publicClient.CloseIdleConnections()
	if axisFound {
		item, isVideo := axis.discovered("VAPIX", "Axis-Geräteinformationen")
		if !isVideo || item.Category != "camera" {
			return result, fmt.Errorf("Axis-Gerät meldet keinen Kameratyp")
		}
		result.Manufacturer, result.Model, result.Firmware, result.DeviceName = item.Manufacturer, item.Model, item.Firmware, axis.Name
		streamURL := url.URL{Scheme: "rtsp", Host: net.JoinHostPort(u.Hostname(), "554"), Path: "/axis-media/media.amp", RawQuery: "videocodec=h264"}
		source := CameraSource{ID: "axis-default", Name: "H.264", URL: streamURL.String(), Codec: "H.264"}
		source.Verified, source.Status = verifyCameraRTSP(ctx, source.URL, creds)
		result.Sources = append(result.Sources, source)
		return result, nil
	}
	client := authenticatedDiscoveryClient()
	defer client.CloseIdleConnections()
	body, status, err := cameraGET(ctx, client, base, hikvisionDevicePath, creds)
	if err != nil {
		return result, err
	}
	if status == 401 || status == 403 {
		return result, fmt.Errorf("Kamerazugang abgelehnt; keine weiteren Anmeldeversuche")
	}
	if status != 200 {
		return result, fmt.Errorf("Geräteinformationen nicht verfügbar (HTTP %d)", status)
	}
	var info struct {
		XMLName      xml.Name `xml:"DeviceInfo"`
		Manufacturer string   `xml:"manufacturer"`
		Model        string   `xml:"model"`
		Firmware     string   `xml:"firmwareVersion"`
		Name         string   `xml:"deviceName"`
		Type         string   `xml:"deviceType"`
	}
	deviceType := ""
	if xml.Unmarshal(body, &info) == nil {
		deviceType = strings.ToLower(strings.ReplaceAll(info.Type, " ", ""))
	}
	if deviceType != "ipcamera" && deviceType != "networkcamera" {
		return result, fmt.Errorf("Gerät liefert keinen bestätigten Kameratyp")
	}
	result.Manufacturer = canonicalManufacturer(info.Manufacturer)
	result.Model = info.Model
	result.Firmware = info.Firmware
	result.DeviceName = info.Name
	if result.Manufacturer == "" && hikvisionCameraModel.MatchString(info.Model) {
		result.Manufacturer = "Hikvision (Modellkennung)"
	}
	port := 554
	if b, code, e := cameraGET(ctx, client, base, "/ISAPI/System/Network/ports", creds); e == nil && code == 200 {
		var ports struct {
			Items []struct {
				Protocol string `xml:"protocol"`
				Port     int    `xml:"portNo"`
			} `xml:"NetworkPort"`
		}
		if xml.Unmarshal(b, &ports) == nil {
			for _, item := range ports.Items {
				if strings.EqualFold(item.Protocol, "RTSP") && item.Port > 0 && item.Port <= 65535 {
					port = item.Port
				}
			}
		}
	}
	if b, code, e := cameraGET(ctx, client, base, "/ISAPI/Streaming/channels", creds); e == nil && code == 200 {
		result.Sources, err = parseCameraSources(b, u.Hostname(), port)
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
		}
	} else {
		result.Warnings = append(result.Warnings, "Streamliste nicht verfügbar")
	}
	for index := range result.Sources {
		if ctx.Err() != nil {
			result.Warnings = append(result.Warnings, "Zeitlimit bei der Streamprüfung erreicht")
			break
		}
		result.Sources[index].Verified, result.Sources[index].Status = verifyCameraRTSP(ctx, result.Sources[index].URL, creds)
		if result.Sources[index].Status == "RTSP-Zugang nicht bestätigt" {
			result.Warnings = append(result.Warnings, "RTSP-Anmeldung abgelehnt; weitere Stream-Anmeldeversuche wurden abgebrochen")
			break
		}
	}
	if len(result.Sources) > 1 {
		result.Options = append(result.Options, "Hochauflösenden Quellstream für detailreiche Entzerrung wählen; kleinere Streams reduzieren die Eingangs-Datenrate")
	}
	for _, source := range result.Sources {
		if source.Audio {
			result.Options = append(result.Options, "Mindestens ein Quellstream bietet Audio; die aktuelle Entzerrungspipeline übernimmt weiterhin nur Video")
			break
		}
	}
	return result, nil
}
