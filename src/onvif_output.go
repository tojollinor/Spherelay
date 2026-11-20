package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	onvifPort       = "8899"
	onvifDevice     = "http://www.onvif.org/ver10/device/wsdl"
	onvifMedia      = "http://www.onvif.org/ver10/media/wsdl"
	onvifSOAP       = "http://www.w3.org/2003/05/soap-envelope"
	onvifWSA        = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	onvifDiscovery  = "http://schemas.xmlsoap.org/ws/2005/04/discovery"
	onvifNamespaces = `xmlns:s="` + onvifSOAP + `" xmlns:a="` + onvifWSA + `" xmlns:d="` + onvifDiscovery + `" xmlns:tds="` + onvifDevice + `" xmlns:trt="` + onvifMedia + `" xmlns:tt="http://www.onvif.org/ver10/schema" xmlns:dn="http://www.onvif.org/ver10/network/wsdl"`
)

type onvifEnvelope struct {
	XMLName xml.Name `xml:"http://www.w3.org/2003/05/soap-envelope Envelope"`
	Header  struct {
		MessageID string `xml:"MessageID"`
		Security  struct {
			Token struct {
				Username string `xml:"Username"`
				Password string `xml:"Password"`
				Nonce    string `xml:"Nonce"`
				Created  string `xml:"Created"`
			} `xml:"UsernameToken"`
		} `xml:"Security"`
	} `xml:"Header"`
	Body struct {
		Operation struct {
			XMLName            xml.Name
			ProfileToken       string
			ConfigurationToken string
			Types              string
			Scopes             string
			Transport          string `xml:"StreamSetup>Transport>Protocol"`
			Stream             string `xml:"StreamSetup>Stream"`
		} `xml:",any"`
	} `xml:"Body"`
}

func onvifEscape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func onvifEndpoint(host, path string) string {
	return "http://" + net.JoinHostPort(host, onvifPort) + "/onvif/" + path
}

func (s *Server) runONVIF(ctx context.Context) {
	s.mu.Lock()
	lan := s.store.NetworkONVIF && s.store.NetworkRTSP && s.store.ViewerPassword != ""
	s.mu.Unlock()
	address := "127.0.0.1"
	if lan {
		address = "0.0.0.0"
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(address, onvifPort))
	if err != nil {
		s.onvifEvent("error", "ONVIF-Listener: "+err.Error())
		return
	}
	s.mu.Lock()
	s.onvifLAN = lan
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.onvifLAN = false; s.mu.Unlock() }()
	server := &http.Server{Handler: http.HandlerFunc(s.serveONVIF), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = server.Close(); _ = listener.Close() })
	defer stop()
	if lan {
		go s.runONVIFDiscovery(ctx)
	}
	s.onvifEvent("info", "ONVIF lauscht auf "+listener.Addr().String())
	if err := server.Serve(listener); err != nil && ctx.Err() == nil {
		s.onvifEvent("error", err.Error())
	}
}

func (s *Server) onvifEvent(level, message string) {
	s.recordDiagnostic(diagnosticEvent{Component: "onvif", Level: level, Message: message})
}

func (s *Server) onvifAuthorized(in onvifEnvelope) bool {
	token := in.Header.Security.Token
	created, err := time.Parse(time.RFC3339Nano, token.Created)
	now := time.Now()
	if err != nil || created.Before(now.Add(-5*time.Minute)) || created.After(now.Add(time.Minute)) || token.Username != "viewer" {
		return false
	}
	nonce, err := base64.StdEncoding.DecodeString(token.Nonce)
	if err != nil || len(nonce) < 8 || len(nonce) > 128 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.onvifLAN || !s.store.NetworkONVIF || !s.store.NetworkRTSP || s.store.ViewerPassword == "" {
		return false
	}
	hash := sha1.New()
	_, _ = hash.Write(nonce)
	_, _ = io.WriteString(hash, token.Created+s.store.ViewerPassword)
	if subtle.ConstantTimeCompare([]byte(token.Password), []byte(base64.StdEncoding.EncodeToString(hash.Sum(nil)))) != 1 {
		return false
	}
	if s.onvifNonces == nil {
		s.onvifNonces = make(map[string]time.Time)
	}
	for key, expires := range s.onvifNonces {
		if now.After(expires) {
			delete(s.onvifNonces, key)
		}
	}
	if _, seen := s.onvifNonces[string(nonce)]; seen || len(s.onvifNonces) >= 8192 {
		return false
	}
	s.onvifNonces[string(nonce)] = created.Add(5 * time.Minute)
	return true
}

func (s *Server) serveONVIF(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Path == "/onvif/device_service"
	if !device && r.URL.Path != "/onvif/media_service" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in onvifEnvelope
	decoder := xml.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	if err := decoder.Decode(&in); err != nil {
		onvifFault(w, http.StatusBadRequest, "InvalidArgVal", "Ungültige SOAP-Nachricht")
		return
	}
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		onvifFault(w, 500, "Action", "Lokale Adresse fehlt")
		return
	}
	host, _, _ := net.SplitHostPort(local.String())
	op := in.Body.Operation
	localClient := net.ParseIP(remote).IsLoopback()
	publicTime := device && op.XMLName.Space == onvifDevice && op.XMLName.Local == "GetSystemDateAndTime"
	if !localClient && ((publicTime && !s.onvifNetworkActive()) || (!publicTime && !s.onvifAuthorized(in))) {
		s.onvifEvent("warn", "ONVIF-Authentifizierung abgelehnt für "+remote)
		onvifFault(w, http.StatusUnauthorized, "NotAuthorized", "ONVIF-Authentifizierung erforderlich")
		return
	}
	namespace, prefix := onvifMedia, "trt:"
	if device {
		namespace, prefix = onvifDevice, "tds:"
	}
	if op.XMLName.Space != namespace {
		onvifFault(w, 400, "ActionNotSupported", "Unbekannter Dienst")
		return
	}
	s.mu.Lock()
	streams := append([]Stream(nil), s.store.Streams...)
	s.mu.Unlock()
	var result string
	var handled bool
	if device {
		result, handled = s.onvifDeviceResponse(op.XMLName.Local, host)
	} else {
		result, handled = onvifMediaResponse(in, streams, host, localClient)
	}
	if !handled {
		onvifFault(w, 400, "ActionNotSupported", "Operation, Profil oder Transport nicht verfügbar")
		return
	}
	s.recordDiagnostic(diagnosticEvent{Component: "onvif", Level: "info", Stream: op.ProfileToken, Message: op.XMLName.Local + " von " + remote})
	name := prefix + op.XMLName.Local + "Response"
	onvifWrite(w, 200, namespace+"/"+op.XMLName.Local+"Response", in.Header.MessageID, "<"+name+">"+result+"</"+name+">")
}

func (s *Server) onvifNetworkActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onvifLAN && s.store.NetworkONVIF && s.store.NetworkRTSP
}

func onvifWrite(w http.ResponseWriter, status int, action, relates, content string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	header := ""
	if relates != "" {
		header = "<s:Header><a:Action>" + onvifEscape(action) + "</a:Action><a:RelatesTo>" + onvifEscape(relates) + "</a:RelatesTo></s:Header>"
	}
	_, _ = io.WriteString(w, "<s:Envelope "+onvifNamespaces+">"+header+"<s:Body>"+content+"</s:Body></s:Envelope>")
}

func onvifFault(w http.ResponseWriter, status int, code, message string) {
	onvifWrite(w, status, "", "", "<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode xmlns:ter=\"http://www.onvif.org/ver10/error\"><s:Value>ter:"+code+"</s:Value></s:Subcode></s:Code><s:Reason><s:Text xml:lang=\"de\">"+onvifEscape(message)+"</s:Text></s:Reason></s:Fault>")
}

func (s *Server) onvifDeviceResponse(operation, host string) (string, bool) {
	device := onvifEscape(onvifEndpoint(host, "device_service"))
	media := onvifEscape(onvifEndpoint(host, "media_service"))
	switch operation {
	case "GetDeviceInformation":
		return "<tds:Manufacturer>Spherelay</tds:Manufacturer><tds:Model>Virtual Camera</tds:Model><tds:FirmwareVersion>" + version + "</tds:FirmwareVersion><tds:SerialNumber>" + s.onvifID() + "</tds:SerialNumber><tds:HardwareId>Spherelay</tds:HardwareId>", true
	case "GetSystemDateAndTime":
		now := time.Now().UTC()
		return fmt.Sprintf("<tds:SystemDateAndTime><tt:DateTimeType>Manual</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings><tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time><tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime></tds:SystemDateAndTime>", now.Hour(), now.Minute(), now.Second(), now.Year(), now.Month(), now.Day()), true
	case "GetServices":
		return "<tds:Service><tds:Namespace>" + onvifDevice + "</tds:Namespace><tds:XAddr>" + device + "</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>" + onvifMedia + "</tds:Namespace><tds:XAddr>" + media + "</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version></tds:Service>", true
	case "GetCapabilities":
		return `<tds:Capabilities><tt:Device><tt:XAddr>` + device + `</tt:XAddr></tt:Device><tt:Media><tt:XAddr>` + media + `</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media></tds:Capabilities>`, true
	case "GetServiceCapabilities":
		return `<tds:Capabilities><tds:Security UsernameToken="true"/></tds:Capabilities>`, true
	case "GetScopes":
		return `<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/type/video_encoder</tt:ScopeItem></tds:Scopes><tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/name/Spherelay</tt:ScopeItem></tds:Scopes>`, true
	}
	return "", false
}

func onvifMediaResponse(in onvifEnvelope, streams []Stream, host string, local bool) (string, bool) {
	op := in.Body.Operation
	var result strings.Builder
	switch op.XMLName.Local {
	case "GetServiceCapabilities":
		return `<trt:Capabilities><trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="true" RTP_RTSP_TCP="true"/></trt:Capabilities>`, true
	case "GetProfiles", "GetProfile", "GetVideoEncoderConfigurations", "GetVideoEncoderConfiguration", "GetVideoSources", "GetVideoSourceConfigurations", "GetVideoSourceConfiguration", "GetStreamUri":
	default:
		return "", false
	}
	for _, view := range streams {
		if !view.Enabled || view.ID == "" {
			continue
		}
		token := onvifEscape(view.ID)
		name := onvifEscape(view.Name)
		resolution := fmt.Sprintf("<tt:Width>%d</tt:Width><tt:Height>%d</tt:Height>", view.Width, view.Height)
		source := fmt.Sprintf(`<tt:Name>%s</tt:Name><tt:UseCount>1</tt:UseCount><tt:SourceToken>%s</tt:SourceToken><tt:Bounds x="0" y="0" width="%d" height="%d"/>`, name, token, view.Width, view.Height)
		const disabledMulticast = "<tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>0</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast>"
		// MediaMTX advertises 55 seconds in SETUP with the default idle timeout.
		encoder := fmt.Sprintf("<tt:Name>%s</tt:Name><tt:UseCount>1</tt:UseCount><tt:Encoding>H264</tt:Encoding><tt:Resolution>%s</tt:Resolution><tt:Quality>33</tt:Quality><tt:H264><tt:GovLength>%d</tt:GovLength><tt:H264Profile>High</tt:H264Profile></tt:H264>%s<tt:SessionTimeout>PT55S</tt:SessionTimeout>", name, resolution, view.FPS*2, disabledMulticast)
		profile := `<tt:Name>` + name + `</tt:Name><tt:VideoSourceConfiguration token="vs-` + token + `">` + source + `</tt:VideoSourceConfiguration><tt:VideoEncoderConfiguration token="ve-` + token + `">` + encoder + `</tt:VideoEncoderConfiguration>`
		switch op.XMLName.Local {
		case "GetProfiles":
			fmt.Fprintf(&result, `<trt:Profiles token="%s" fixed="true">%s</trt:Profiles>`, token, profile)
		case "GetProfile":
			if op.ProfileToken == view.ID {
				return fmt.Sprintf(`<trt:Profile token="%s" fixed="true">%s</trt:Profile>`, token, profile), true
			}
		case "GetVideoEncoderConfigurations":
			fmt.Fprintf(&result, `<trt:Configurations token="ve-%s">%s</trt:Configurations>`, token, encoder)
		case "GetVideoEncoderConfiguration":
			if op.ConfigurationToken == "ve-"+view.ID {
				return fmt.Sprintf(`<trt:Configuration token="ve-%s">%s</trt:Configuration>`, token, encoder), true
			}
		case "GetVideoSources":
			fmt.Fprintf(&result, `<trt:VideoSources token="%s"><tt:Framerate>%d</tt:Framerate><tt:Resolution>%s</tt:Resolution></trt:VideoSources>`, token, view.FPS, resolution)
		case "GetVideoSourceConfigurations":
			fmt.Fprintf(&result, `<trt:Configurations token="vs-%s">%s</trt:Configurations>`, token, source)
		case "GetVideoSourceConfiguration":
			if op.ConfigurationToken == "vs-"+view.ID {
				return fmt.Sprintf(`<trt:Configuration token="vs-%s">%s</trt:Configuration>`, token, source), true
			}
		case "GetStreamUri":
			if op.ProfileToken != view.ID {
				continue
			}
			if op.Stream != "" && op.Stream != "RTP-Unicast" || op.Transport != "" && op.Transport != "TCP" && op.Transport != "RTSP" {
				return "", false
			}
			port := "8554"
			if local {
				port = "8555"
			}
			uri := url.URL{Scheme: "rtsp", Host: net.JoinHostPort(host, port), Path: "/" + view.ID}
			return "<trt:MediaUri><tt:Uri>" + onvifEscape(uri.String()) + "</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT0S</tt:Timeout></trt:MediaUri>", true
		}
	}
	return result.String(), strings.HasSuffix(op.XMLName.Local, "s")
}

func (s *Server) onvifID() string {
	s.mu.Lock()
	salt := s.store.Salt
	s.mu.Unlock()
	hash := sha1.Sum([]byte("spherelay-onvif:" + salt))
	hash[6] = (hash[6] & 0x0f) | 0x50
	hash[8] = (hash[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", hash[:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}

func (s *Server) runONVIFDiscovery(ctx context.Context) {
	interfaces, err := net.Interfaces()
	if err != nil {
		s.onvifEvent("error", err.Error())
		return
	}
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, addr := range addresses {
			ip, subnet, _ := net.ParseCIDR(addr.String())
			if ip.To4() == nil {
				continue
			}
			go s.serveONVIFDiscovery(ctx, iface, ip.String(), subnet)
			break
		}
	}
}

func (s *Server) serveONVIFDiscovery(ctx context.Context, iface net.Interface, host string, subnet *net.IPNet) {
	conn, err := net.ListenMulticastUDP("udp4", &iface, &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702})
	if err != nil {
		s.onvifEvent("warn", "WS-Discovery "+iface.Name+": "+err.Error())
		return
	}
	defer conn.Close()
	// Winsock applies IP_MULTICAST_LOOP to the receiver. Go clears it when
	// joining, so restore it for clients running on the same Windows host.
	if err := enableONVIFMulticastLoopback(conn); err != nil {
		s.onvifEvent("warn", "WS-Discovery Loopback "+iface.Name+": "+err.Error())
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	buf := make([]byte, 8192)
	instance := time.Now().Unix()
	var messageNumber uint64
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if !subnet.Contains(peer.IP) || !s.onvifNetworkActive() {
			continue
		}
		var in onvifEnvelope
		if xml.Unmarshal(buf[:n], &in) != nil || in.Header.MessageID == "" || in.Body.Operation.XMLName.Space != onvifDiscovery {
			continue
		}
		op := in.Body.Operation
		if op.XMLName.Local != "Probe" || op.Types != "" && !strings.Contains(op.Types, ":NetworkVideoTransmitter") && !strings.Contains(op.Types, ":Device") {
			continue
		}
		match := true
		for _, scope := range strings.Fields(op.Scopes) {
			if scope != "onvif://www.onvif.org/type/video_encoder" && scope != "onvif://www.onvif.org/name/Spherelay" {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		messageNumber++
		sequence := fmt.Sprintf(`<d:AppSequence InstanceId="%d" MessageNumber="%d"/>`, instance, messageNumber)
		data := "<s:Envelope " + onvifNamespaces + "><s:Header><a:Action>" + onvifDiscovery + "/ProbeMatches</a:Action><a:MessageID>urn:uuid:" + discoveryUUID() + "</a:MessageID><a:RelatesTo>" + onvifEscape(in.Header.MessageID) + "</a:RelatesTo><a:To>" + onvifWSA + "/role/anonymous</a:To>" + sequence + "</s:Header><s:Body><d:ProbeMatches><d:ProbeMatch><a:EndpointReference><a:Address>urn:uuid:" + s.onvifID() + "</a:Address></a:EndpointReference><d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types><d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/name/Spherelay</d:Scopes><d:XAddrs>" + onvifEscape(onvifEndpoint(host, "device_service")) + "</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches></s:Body></s:Envelope>"
		if _, err := conn.WriteToUDP([]byte(data), peer); err == nil {
			s.onvifEvent("debug", "WS-Discovery Probe beantwortet für "+peer.String())
		}
	}
}
