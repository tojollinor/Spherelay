package main

import "strings"

const cameraSDPTestURL = "rtsp://" + rtspPlayTestAddress + "/view-huJ_ydm0-original"

// This fixture applies only to the single HEVC stream used for the A/B test.
// Keep the origin supplied by MediaMTX and the published codec parameters.
func originalCameraSDP(current string) (string, bool) {
	const vps = "sprop-vps=QAEMAf//AWAAAAMAAAMAAAMAAAMAmawJ"
	const sps = "sprop-sps=QgEBAWAAAAMAAAMAAAMAAAMAmaAB4CACHH+KrTuiS7/8AAQABbAgApMuADN/mAE="
	const pps = "sprop-pps=RAHAcvCcFAobJA=="
	if !strings.Contains(current, "a=rtpmap:96 H265/90000") ||
		!strings.Contains(current, vps) || !strings.Contains(current, sps) || !strings.Contains(current, pps) {
		return "", false
	}
	origin := "o=- 0 0 IN IP4 127.0.0.1"
	for _, line := range strings.Split(current, "\n") {
		if strings.HasPrefix(line, "o=") {
			origin = strings.TrimSuffix(line, "\r")
			break
		}
	}
	return strings.Join([]string{
		"v=0", origin, "s=Media Presentation", "t=0 0",
		"a=control:" + cameraSDPTestURL + "/",
		"m=video 0 RTP/AVP 96", "c=IN IP4 0.0.0.0", "a=recvonly",
		"a=x-dimensions:3840,2160", "a=control:" + cameraSDPTestURL + "/trackID=1",
		"a=rtpmap:96 H265/90000", "a=fmtp:96 " + vps + "; " + sps + "; " + pps,
		"a=Media_header:MEDIAINFO=494D4B48010300000400050000000000000000000000000000000000000000000000000000000000;",
		"a=appversion:1.0", "",
	}, "\r\n"), true
}
