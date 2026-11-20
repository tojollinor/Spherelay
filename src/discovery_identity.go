package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"sync"
)

const axisBrandPath = "/axis-cgi/param.cgi?action=list&group=Brand"
const axisDevicePath = "/axis-cgi/basicdeviceinfo.cgi"
const hikvisionDevicePath = "/ISAPI/System/deviceInfo"

type cameraIdentity struct {
	Manufacturer string
	Model        string
	DeviceType   string
	Firmware     string
	Name         string
}

func canonicalManufacturer(name string) string {
	name = strings.TrimSpace(name)
	switch strings.ToLower(name) {
	case "axis", "axis communications", "axis communications ab":
		return "Axis"
	case "hikvision", "hangzhou hikvision digital technology co., ltd.":
		return "Hikvision"
	case "dahua", "dahua technology", "zhejiang dahua technology co., ltd.":
		return "Dahua"
	case "hanwha", "hanwha vision", "hanwha techwin":
		return "Hanwha Vision"
	case "bosch", "bosch security systems":
		return "Bosch"
	case "vivotek":
		return "Vivotek"
	case "uniview":
		return "Uniview"
	}
	return name
}

func (identity cameraIdentity) discovered(method, evidence string) (Discovered, bool) {
	kind := strings.TrimSpace(identity.DeviceType)
	compact := strings.ToLower(strings.ReplaceAll(kind, " ", ""))
	category, label := "camera", "IP-Kamera"
	switch {
	case recorderWords.MatchString(kind):
		category, label = "video-device", "Videorekorder"
	case compact == "ipcamera", compact == "networkcamera", cameraTypeWord.MatchString(kind):
	default:
		return Discovered{}, false
	}
	return Discovered{
		Type: label, Category: category, Method: method, Evidence: evidence,
		Model: strings.TrimSpace(identity.Model), Firmware: strings.TrimSpace(identity.Firmware),
		Manufacturer: canonicalManufacturer(identity.Manufacturer),
	}, true
}

func parseAxisIdentity(data []byte) (cameraIdentity, bool) {
	var reply struct {
		Data struct {
			Properties struct {
				Brand    string `json:"Brand"`
				Model    string `json:"ProdNbr"`
				Type     string `json:"ProdType"`
				Firmware string `json:"Version"`
				Name     string `json:"ProdFullName"`
			} `json:"propertyList"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return cameraIdentity{}, false
	}
	p := reply.Data.Properties
	if canonicalManufacturer(p.Brand) != "Axis" {
		return cameraIdentity{}, false
	}
	return cameraIdentity{Manufacturer: p.Brand, Model: p.Model, DeviceType: p.Type, Firmware: p.Firmware, Name: p.Name}, true
}

func readAxisIdentity(ctx context.Context, client *http.Client, base string) (cameraIdentity, bool) {
	const query = `{"apiVersion":"1.0","method":"getAllUnrestrictedProperties"}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+axisDevicePath, strings.NewReader(query))
	if err != nil {
		return cameraIdentity{}, false
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return cameraIdentity{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return cameraIdentity{}, false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return cameraIdentity{}, false
	}
	return parseAxisIdentity(body)
}

func readONVIFIdentity(ctx context.Context, client *http.Client, device *Discovered) {
	const query = `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><GetDeviceInformation xmlns="http://www.onvif.org/ver10/device/wsdl"/></s:Body></s:Envelope>`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, device.XAddr, strings.NewReader(query))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	response, err := client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return
	}
	var envelope struct {
		Body struct {
			Information struct {
				Manufacturer string
				Model        string
				Firmware     string `xml:"FirmwareVersion"`
			} `xml:"http://www.onvif.org/ver10/device/wsdl GetDeviceInformationResponse"`
		} `xml:"http://www.w3.org/2003/05/soap-envelope Body"`
	}
	if xml.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope) != nil {
		return
	}
	info := envelope.Body.Information
	if info.Manufacturer != "" {
		device.Manufacturer = canonicalManufacturer(info.Manufacturer)
	}
	if info.Model != "" {
		device.Model = strings.TrimSpace(info.Model)
	}
	device.Firmware = strings.TrimSpace(info.Firmware)
}

func enrichONVIFIdentities(ctx context.Context, devices []Discovered) {
	client := discoveryClient()
	defer client.CloseIdleConnections()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(devices)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() == nil {
					readONVIFIdentity(ctx, client, &devices[index])
				}
			}
		}()
	}
	for index := range devices {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
}
