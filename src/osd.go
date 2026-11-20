package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type OSDConfig struct {
	CameraName    bool     `json:"cameraName"`
	ViewName      bool     `json:"viewName"`
	Clock         bool     `json:"clock"`
	TitlePosition string   `json:"titlePosition,omitempty"`
	ClockPosition string   `json:"clockPosition,omitempty"`
	ClockFormat   string   `json:"clockFormat,omitempty"`
	SizePercent   float64  `json:"sizePercent,omitempty"`
	MarginPercent *float64 `json:"marginPercent,omitempty"`
	Color         string   `json:"color,omitempty"`
	Font          string   `json:"font,omitempty"`
	Style         string   `json:"style,omitempty"`
}

func effectiveOSD(v Stream) OSDConfig {
	o := OSDConfig{ViewName: true, Clock: true}
	if v.OSD != nil {
		o = *v.OSD
	}
	if o.TitlePosition == "" {
		o.TitlePosition = "bottom-right"
	}
	if o.ClockPosition == "" {
		o.ClockPosition = "top-left"
	}
	if o.ClockFormat == "" {
		o.ClockFormat = "dmy-weekday"
	}
	if o.SizePercent == 0 {
		o.SizePercent = 2.8
	}
	if o.MarginPercent == nil {
		m := 2.
		o.MarginPercent = &m
	}
	if o.Color != "#ffffff" && o.Color != "#000000" && o.Color != "auto" {
		o.Color = "#ffffff"
	}
	if o.Font == "" {
		o.Font = "mono"
	}
	if o.Style == "" {
		o.Style = "outline"
	}
	return o
}
func validOSD(v Stream) bool {
	if v.OSD == nil {
		return true
	}
	o := effectiveOSD(v)
	if v.OSD.Color != "" && v.OSD.Color != "#ffffff" && v.OSD.Color != "#000000" && v.OSD.Color != "auto" {
		return false
	}
	position := func(s string) bool {
		switch s {
		case "top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right":
			return true
		}
		return false
	}
	return position(o.TitlePosition) && position(o.ClockPosition) && validClockFormat(o.ClockFormat) && o.SizePercent >= 1 && o.SizePercent <= 8 && *o.MarginPercent >= 0 && *o.MarginPercent <= 10 && (osdColor.MatchString(o.Color) || o.Color == "auto") && (o.Font == "mono" || o.Font == "sans") && (o.Style == "outline" || o.Style == "box" || o.Style == "plain")
}

var osdColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validClockFormat(f string) bool {
	return f == "time" || f == "dmy" || f == "dmy-weekday" || f == "iso"
}
func formatOSDTime(t time.Time, f string) string {
	switch f {
	case "time":
		return t.Format("15:04:05")
	case "dmy":
		return t.Format("02-01-2006 15:04:05")
	case "iso":
		return t.Format("2006-01-02 15:04:05")
	default:
		return t.Format("02-01-2006 Mon 15:04:05")
	}
}

type pipelineOptions struct {
	Circle     LensCircle
	Titles     map[string]string
	Clock      string
	Font       string
	Fonts      map[string]string
	ClockFiles map[string]string
}

func filterFile(path string) string {
	return "'" + strings.NewReplacer("\\", "/", ":", "\\:", "'", "'\\''").Replace(path) + "'"
}

func (s *Server) preparePipelineOptions(camera Camera, views []Stream) (pipelineOptions, error) {
	options := pipelineOptions{Circle: s.cameraCircle(camera), Titles: map[string]string{}}
	dir := filepath.Join(s.dataDir, "osd")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return options, err
	}
	options.Clock = filepath.Join(dir, "clock.txt")
	options.ClockFiles = map[string]string{}
	options.Fonts = map[string]string{}
	for kind, candidates := range map[string][]string{"mono": {filepath.Join(os.Getenv("WINDIR"), "Fonts", "consola.ttf"), "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"}, "sans": {filepath.Join(os.Getenv("WINDIR"), "Fonts", "arial.ttf"), "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"}} {
		for _, candidate := range candidates {
			if fileExists(candidate) {
				options.Fonts[kind] = candidate
				break
			}
		}
	}
	options.Font = options.Fonts["sans"]
	for _, view := range views {
		osd := effectiveOSD(view)
		names := []string{}
		if osd.CameraName {
			names = append(names, camera.Name)
		}
		if osd.ViewName {
			names = append(names, view.Name)
		}
		if len(names) > 0 {
			sum := sha256.Sum256([]byte(view.ID))
			path := filepath.Join(dir, fmt.Sprintf("title-%x.txt", sum[:10]))
			if err := os.WriteFile(path, []byte(strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.Join(names, " · "))), 0600); err != nil {
				return options, err
			}
			options.Titles[view.ID] = path
		}
	}
	return options, nil
}

func writeOSDText(path, text string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".osd-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}
func writeOSDClock(path string) error {
	return writeOSDText(path, formatOSDTime(time.Now(), "dmy-weekday"))
}
func writeAllClocks(dir string, now time.Time) error {
	if err := writeOSDText(filepath.Join(dir, "clock.txt"), formatOSDTime(now, "dmy-weekday")); err != nil {
		return err
	}
	for _, format := range []string{"time", "dmy", "dmy-weekday", "iso"} {
		if err := writeOSDText(filepath.Join(dir, "clock-"+format+".txt"), formatOSDTime(now, format)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) runOSDClock(ctx context.Context) {
	dir := filepath.Join(s.dataDir, "osd")
	if os.MkdirAll(dir, 0700) != nil {
		return
	}
	writeAllClocks(dir, time.Now())
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			writeAllClocks(dir, now)
		}
	}
}
func osdPosition(position string, margin float64) string {
	m := fmt.Sprintf("h*%.4f", margin/100)
	x := ""
	switch {
	case strings.HasSuffix(position, "left"):
		x = m
	case strings.HasSuffix(position, "center"):
		x = "(w-tw)/2"
	default:
		x = "w-tw-" + m
	}
	y := m
	if strings.HasPrefix(position, "bottom") {
		y = "h-th-" + m
	}
	return ":x=" + x + ":y=" + y
}
func osdFilters(view Stream, options pipelineOptions) string {
	o := effectiveOSD(view)
	common := "drawtext="
	font := options.Font
	if options.Fonts[o.Font] != "" {
		font = options.Fonts[o.Font]
	}
	if font != "" {
		common += "fontfile=" + filterFile(font) + ":"
	}
	common += fmt.Sprintf("expansion=none:fontcolor=0x%s:fontsize=h*%.4f:", strings.TrimPrefix(o.Color, "#"), o.SizePercent/100)
	switch o.Style {
	case "box":
		if o.Color == "#000000" {
			common += "box=1:boxcolor=white@0.5:boxborderw=4:"
		} else {
			common += "box=1:boxcolor=black@0.5:boxborderw=4:"
		}
	case "outline":
		if o.Color == "#000000" {
			common += "borderw=1:bordercolor=white:"
		} else {
			common += "borderw=1:bordercolor=black:"
		}
	}
	filters := ""
	if path := options.Titles[view.ID]; path != "" {
		filters += "," + common + "textfile=" + filterFile(path) + osdPosition(o.TitlePosition, *o.MarginPercent)
	}
	if o.Clock {
		// Render wall-clock time inside FFmpeg. Reloading an atomically replaced
		// file can fail with EACCES on Windows and terminate the entire pipeline.
		formats := map[string]string{"time": "%T", "dmy": "%d-%m-%Y %T", "dmy-weekday": "%d-%m-%Y %a %T", "iso": "%Y-%m-%d %T"}
		format := formats[o.ClockFormat]
		if format == "" {
			format = formats["dmy-weekday"]
		}
		clockStyle := strings.Replace(common, "expansion=none:", "expansion=normal:", 1)
		filters += "," + clockStyle + "text='%{localtime\\:" + format + "}'" + osdPosition(o.ClockPosition, *o.MarginPercent)
	}
	return filters
}

// The text mask selects black or white from the underlying luminance, after mirroring.
func adaptiveOSDGraph(view Stream, options pipelineOptions, index int) string {
	o := effectiveOSD(view)
	o.Color = "#ffffff"
	o.Style = "plain"
	maskView := view
	maskView.OSD = &o
	mask := osdFilters(maskView, options)
	p := fmt.Sprintf("osd%d", index)
	if mask == "" {
		return fmt.Sprintf(";[%sbase]format=yuv420p[v%d]", p, index)
	}
	return fmt.Sprintf(";[%sbase]split=3[%ssrc][%sluma][%smasksrc];[%ssrc]format=gbrp[%soriginal];[%sluma]format=gray,lut=y='if(lt(val,128),255,0)',format=gbrp[%scontrast];[%smasksrc]format=gray,lut=y=0%s,format=gbrp[%smask];[%soriginal][%scontrast][%smask]maskedmerge,format=yuv420p[v%d]", p, p, p, p, p, p, p, p, p, mask, p, p, p, p, index)
}
