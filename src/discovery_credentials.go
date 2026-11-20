package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

type DiscoveryCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// A separate client validates HTTPS certificates for all authenticated requests.
// Authorization is never attached to a redirect or to an anonymous HTTP scan.
func authenticatedDiscoveryClient() *http.Client {
	client := discoveryClient()
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

var digestParameter = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*)\s*=\s*("(?:[^"\\]|\\.)*"|[^,\s]+)`)

func parseDigest(value string) (map[string]string, bool) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Digest") {
		return nil, false
	}
	fields := map[string]string{}
	for _, match := range digestParameter.FindAllStringSubmatch(rest, -1) {
		val := match[2]
		if strings.HasPrefix(val, `"`) {
			var b strings.Builder
			for i := 1; i < len(val)-1; i++ {
				if val[i] == '\\' && i+1 < len(val)-1 {
					i++
				}
				b.WriteByte(val[i])
			}
			val = b.String()
		}
		fields[strings.ToLower(match[1])] = val
	}
	return fields, fields["realm"] != "" && fields["nonce"] != ""
}

func digestHash(algorithm, data string) (string, error) {
	var bytes []byte
	switch strings.ToUpper(strings.TrimSuffix(strings.ToUpper(algorithm), "-SESS")) {
	case "MD5":
		v := md5.Sum([]byte(data))
		bytes = v[:]
	case "SHA-256":
		v := sha256.Sum256([]byte(data))
		bytes = v[:]
	case "SHA-512-256":
		v := sha512.Sum512_256([]byte(data))
		bytes = v[:]
	default:
		return "", fmt.Errorf("Nicht unterstütztes Kamera-Anmeldeverfahren")
	}
	return hex.EncodeToString(bytes), nil
}

func quoteDigest(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func digestAuthorization(challenge, method, uri string, creds DiscoveryCredentials, cnonce string) (string, error) {
	fields, ok := parseDigest(challenge)
	if !ok {
		return "", fmt.Errorf("Ungültige Kamera-Anmeldeaufforderung")
	}
	if len(fields["realm"]) > 512 || len(fields["nonce"]) > 2048 || strings.ContainsAny(challenge, "\r\n") {
		return "", fmt.Errorf("Ungültige Kamera-Anmeldeaufforderung")
	}
	algorithm := fields["algorithm"]
	if algorithm == "" {
		algorithm = "MD5"
	}
	hash := func(v string) string { x, _ := digestHash(algorithm, v); return x }
	if _, err := digestHash(algorithm, ""); err != nil {
		return "", err
	}
	qop := ""
	if fields["qop"] != "" {
		for _, choice := range strings.Split(fields["qop"], ",") {
			if strings.TrimSpace(choice) == "auth" {
				qop = "auth"
			}
		}
		if qop == "" {
			return "", fmt.Errorf("Kamera unterstützt kein Digest-auth")
		}
	}
	ha1 := hash(creds.Username + ":" + fields["realm"] + ":" + creds.Password)
	if strings.HasSuffix(strings.ToUpper(algorithm), "-SESS") {
		ha1 = hash(ha1 + ":" + fields["nonce"] + ":" + cnonce)
	}
	ha2 := hash(method + ":" + uri)
	response := hash(ha1 + ":" + fields["nonce"] + ":" + ha2)
	if qop != "" {
		response = hash(ha1 + ":" + fields["nonce"] + ":00000001:" + cnonce + ":" + qop + ":" + ha2)
	}
	username := creds.Username
	if fields["userhash"] == "true" {
		username = hash(creds.Username + ":" + fields["realm"])
	}
	values := []string{"username=" + quoteDigest(username), "realm=" + quoteDigest(fields["realm"]), "nonce=" + quoteDigest(fields["nonce"]), "uri=" + quoteDigest(uri), "response=" + quoteDigest(response), "algorithm=" + algorithm}
	if fields["algorithm"] == "" {
		values = values[:len(values)-1]
	}
	if qop != "" {
		values = append(values, "qop=auth", "nc=00000001")
	}
	if qop != "" || strings.HasSuffix(strings.ToUpper(algorithm), "-SESS") {
		values = append(values, "cnonce="+quoteDigest(cnonce))
	}
	if fields["opaque"] != "" {
		values = append(values, "opaque="+quoteDigest(fields["opaque"]))
	}
	if fields["userhash"] == "true" {
		values = append(values, "userhash=true")
	}
	return "Digest " + strings.Join(values, ", "), nil
}

// Only the already identified candidate's exact device-info endpoint is retried.
// One credential attempt per IP per scan; no password lists or automatic retries.
func authenticateCamera(ctx context.Context, client *http.Client, candidate Discovered, creds DiscoveryCredentials) (*Discovered, string) {
	base := candidate.XAddr
	path := hikvisionDevicePath
	if candidate.InfoPath == axisBrandPath {
		path = candidate.InfoPath
	}
	if strings.Contains(base, "/axis-cgi/") {
		return nil, "Geräteadresse ungültig"
	}
	request, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return nil, "Geräteadresse ungültig"
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "Geschützte Geräteabfrage nicht erreichbar (ggf. HTTPS-Zertifikat prüfen)"
	}
	anonymousBody, readErr := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	response.Body.Close()
	if response.StatusCode == 200 && readErr == nil {
		if found, ok := identifyCamera(path, 200, response.Header, string(anonymousBody)); ok {
			found.Address = request.URL.Hostname()
			found.XAddr = base
			found.AuthStatus = "not-needed"
			return &found, "Geräteinformationen öffentlich gelesen; keine Zugangsdaten gesendet"
		}
	}
	if response.StatusCode != 401 {
		return nil, "Geräteinformationen bieten kein unterstütztes Anmeldeverfahren"
	}
	authorization := ""
	for _, challenge := range response.Header.Values("WWW-Authenticate") {
		if value, err := digestAuthorization(challenge, "GET", path, creds, token(18)); err == nil {
			authorization = value
			break
		}
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	} else {
		basic := false
		for _, challenge := range response.Header.Values("WWW-Authenticate") {
			if strings.HasPrefix(strings.ToLower(challenge), "basic ") {
				basic = true
			}
		}
		if !basic || request.URL.Scheme != "https" {
			return nil, "Kein unterstütztes Anmeldeverfahren (Basic nur über geprüftes HTTPS)"
		}
		request.SetBasicAuth(creds.Username, creds.Password)
	}
	request = request.Clone(ctx)
	response, err = client.Do(request)
	if err != nil {
		return nil, "Geschützte Geräteabfrage fehlgeschlagen"
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return nil, "Zugangsdaten abgelehnt; kein weiterer Versuch in dieser Suche"
	}
	if err != nil {
		return nil, "Geräteantwort konnte nicht gelesen werden"
	}
	found, ok := identifyCamera(path, response.StatusCode, response.Header, string(body))
	if !ok {
		return nil, "Anmeldung beantwortet, aber kein eindeutiger Kameratyp in den Geräteinformationen"
	}
	found.Address = request.URL.Hostname()
	found.XAddr = base
	found.AuthStatus = "success"
	found.Method = "HTTP-Geräteinfo mit Anmeldung"
	return &found, "Geräteinformationen erfolgreich mit Anmeldung gelesen"
}

func authenticateDiscovered(ctx context.Context, devices []Discovered, creds DiscoveryCredentials) {
	type job struct {
		index  int
		device Discovered
	}
	candidates := []job{}
	seen := map[string]bool{}
	for index, device := range devices {
		if discoveryRank(device.Category) >= 2 && !seen[device.Address] && (device.AuthRequired || strings.Contains(device.Method, "SADP")) {
			candidates = append(candidates, job{index, device})
			seen[device.Address] = true
		}
	}
	client := authenticatedDiscoveryClient()
	defer client.CloseIdleConnections()
	jobs := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range jobs {
				if ctx.Err() != nil {
					return
				}
				found, message := authenticateCamera(ctx, client, task.device, creds)
				device := task.device
				device.AuthStatus = "failed"
				device.AuthMessage = message
				if found != nil {
					device = mergeDiscovered(*found, device)
					device.AuthStatus = found.AuthStatus
					device.AuthMessage = message
					device.AuthRequired = false
				}
				devices[task.index] = device
			}
		}()
	}
send:
	for _, candidate := range candidates {
		select {
		case jobs <- candidate:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
}
