package main

import (
	"bytes"
	"image/jpeg"
	"math"
	"sort"
	"time"
)

type LensCircle struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Radius   float64 `json:"radius"`
	Detected bool    `json:"detected"`
}

func defaultCircle() LensCircle { return LensCircle{X: .5, Y: .5, Radius: 1} }

type circlePoint struct{ x, y float64 }

// Fit the lens boundary itself; scene brightness must not move the optical centre.
func fitCircle(points []circlePoint) (float64, float64, float64, bool) {
	var m [3][4]float64
	for _, p := range points {
		v := [3]float64{p.x, p.y, 1}
		target := -(p.x*p.x + p.y*p.y)
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				m[i][j] += v[i] * v[j]
			}
			m[i][3] += v[i] * target
		}
	}
	for i := 0; i < 3; i++ {
		pivot := i
		for j := i + 1; j < 3; j++ {
			if math.Abs(m[j][i]) > math.Abs(m[pivot][i]) {
				pivot = j
			}
		}
		m[i], m[pivot] = m[pivot], m[i]
		if math.Abs(m[i][i]) < 1e-10 {
			return 0, 0, 0, false
		}
		d := m[i][i]
		for k := i; k < 4; k++ {
			m[i][k] /= d
		}
		for j := 0; j < 3; j++ {
			if j == i {
				continue
			}
			d = m[j][i]
			for k := i; k < 4; k++ {
				m[j][k] -= d * m[i][k]
			}
		}
	}
	x, y := -m[0][3]/2, -m[1][3]/2
	r2 := x*x + y*y - m[2][3]
	if r2 <= 0 {
		return 0, 0, 0, false
	}
	return x, y, math.Sqrt(r2), true
}
func detectLensCircle(data []byte) LensCircle {
	im, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return defaultCircle()
	}
	bounds := im.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w != h || w < 100 {
		return defaultCircle()
	}
	lit := func(x, y int) bool {
		if x < 0 || y < 0 || x >= w || y >= h {
			return false
		}
		r, g, b, _ := im.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
		return math.Max(float64(r), math.Max(float64(g), float64(b))) > 38*257
	}
	points := []circlePoint{}
	for i := 0; i < 96; i++ {
		a := 2 * math.Pi * float64(i) / 96
		last := 0.
		for r := float64(w) * .18; r < float64(w)*.6; r += math.Max(1, float64(w)/800) {
			if lit(int(float64(w)/2+r*math.Cos(a)), int(float64(h)/2+r*math.Sin(a))) {
				last = r
			}
		}
		if last > 0 {
			points = append(points, circlePoint{last / float64(w) * math.Cos(a), last / float64(w) * math.Sin(a)})
		}
	}
	if len(points) < 64 {
		return defaultCircle()
	}
	kept := append([]circlePoint(nil), points...)
	var cx, cy, r float64
	for iteration := 0; iteration < 5; iteration++ {
		var ok bool
		cx, cy, r, ok = fitCircle(kept)
		if !ok {
			return defaultCircle()
		}
		sort.Slice(points, func(i, j int) bool {
			return math.Abs(math.Hypot(points[i].x-cx, points[i].y-cy)-r) < math.Abs(math.Hypot(points[j].x-cx, points[j].y-cy)-r)
		})
		kept = append([]circlePoint(nil), points[:len(points)*3/4]...)
	}
	if r < .3 || r > .51 || math.Abs(cx) > .1 || math.Abs(cy) > .1 {
		return defaultCircle()
	}
	consistent := 0
	for _, p := range points {
		if math.Abs(math.Hypot(p.x-cx, p.y-cy)-r) < .012 {
			consistent++
		}
	}
	if consistent < 64 {
		return defaultCircle()
	}
	outerDark, samples := 0, 0
	for i := 0; i < 96; i++ {
		a := 2 * math.Pi * float64(i) / 96
		x, y := int((.5+cx+(r+.025)*math.Cos(a))*float64(w)), int((.5+cy+(r+.025)*math.Sin(a))*float64(h))
		if x >= 0 && y >= 0 && x < w && y < h {
			samples++
			if !lit(x, y) {
				outerDark++
			}
		}
	}
	if samples < 40 || outerDark*5 < samples*4 {
		return defaultCircle()
	}
	radius := math.Min(2*(r-.004), 2*math.Min(math.Min(.5+cx, .5+cy), math.Min(.5-cx, .5-cy)))
	return LensCircle{X: .5 + cx, Y: .5 + cy, Radius: radius, Detected: true}
}

func (s *Server) rememberCircle(id string, p *cameraPreview, data []byte) {
	p.mu.Lock()
	known := p.circle.Radius > 0
	p.mu.Unlock()
	if known {
		return
	}
	circle := detectLensCircle(data)
	p.mu.Lock()
	p.circle = circle
	p.mu.Unlock()
	s.mu.Lock()
	if s.circles == nil {
		s.circles = map[string]LensCircle{}
	}
	s.circles[id] = circle
	s.mu.Unlock()
}

func (s *Server) cameraCircle(camera Camera) LensCircle {
	if camera.Projection != "fisheye" {
		return defaultCircle()
	}
	s.mu.Lock()
	circle, ok := s.circles[camera.ID]
	s.mu.Unlock()
	if ok {
		return circle
	}
	p, release, err := s.acquirePreview(camera.ID)
	if err != nil {
		return defaultCircle()
	}
	defer release()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	for {
		p.mu.Lock()
		circle, changed, finished := p.circle, p.changed, p.finished
		p.mu.Unlock()
		if circle.Radius > 0 {
			return circle
		}
		if finished {
			return defaultCircle()
		}
		select {
		case <-timer.C:
			return defaultCircle()
		case <-changed:
		}
	}
}

// Perspective image planes use tangent lengths, not a linear ratio of angles.
func verticalFOV(v Stream) float64 {
	return 2 * math.Atan(math.Tan(v.FOV*math.Pi/360)*float64(v.Height)/float64(v.Width)) * 180 / math.Pi
}
func mirrorFilters(v Stream) string {
	f := ""
	if v.MirrorHorizontal {
		f += ",hflip"
	}
	if v.MirrorVertical {
		f += ",vflip"
	}
	return f
}

func streamRay(x, y float64, v Stream) (float64, float64, float64) {
	d := math.Pi / 180
	roll, yaw, pitch := v.Roll*d, v.Yaw*d, v.Pitch*d
	a, b := x*math.Tan(v.FOV*d/2), y*math.Tan(verticalFOV(v)*d/2)
	rx, ry := math.Cos(roll)*a-math.Sin(roll)*b, math.Sin(roll)*a+math.Cos(roll)*b
	py, pz := math.Cos(pitch)*ry-math.Sin(pitch), math.Sin(pitch)*ry+math.Cos(pitch)
	return math.Cos(yaw)*rx + math.Sin(yaw)*pz, py, -math.Sin(yaw)*rx + math.Cos(yaw)*pz
}

// radialRoll aligns the view's lower edge away from the optical centre.
// The centre has no radial direction; use the stable unrotated pose there.
func radialRoll(v Stream) float64 {
	d := math.Pi / 180
	yaw, pitch := v.Yaw*d, v.Pitch*d
	x, y, z := math.Sin(yaw)*math.Cos(pitch), -math.Sin(pitch), math.Cos(yaw)*math.Cos(pitch)
	r := math.Hypot(x, y)
	if r < 1e-8 {
		return 0
	}
	tx, ty, tz := z*x/r, z*y/r, -r
	alongX := tx*math.Cos(yaw) - tz*math.Sin(yaw)
	alongY := tx*math.Sin(yaw)*math.Sin(pitch) + ty*math.Cos(pitch) + tz*math.Cos(yaw)*math.Sin(pitch)
	return math.Atan2(-alongX, alongY) / d
}

func constrainFisheye(v Stream) Stream {
	v.Roll = radialRoll(v)
	fits := func(candidate Stream) bool {
		for _, x := range []float64{-1, 1} {
			for _, y := range []float64{-1, 1} {
				_, _, z := streamRay(x, y, candidate)
				if z < .012 {
					return false
				}
			}
		}
		return true
	}
	if fits(v) {
		return v
	}
	d := math.Pi / 180
	x, y, z := streamRay(0, 0, v)
	angle := math.Atan2(math.Hypot(x, y), z)
	azimuth := math.Atan2(y, x)
	low, high := 0.0, angle
	for i := 0; i < 32; i++ {
		mid := (low + high) / 2
		px, py, pz := math.Sin(mid)*math.Cos(azimuth), math.Sin(mid)*math.Sin(azimuth), math.Cos(mid)
		candidate := v
		candidate.Yaw = math.Atan2(px, pz) / d
		candidate.Pitch = -math.Asin(py) / d
		candidate.Roll = radialRoll(candidate)
		if fits(candidate) {
			low = mid
		} else {
			high = mid
		}
	}
	v.Yaw = math.Atan2(math.Sin(low)*math.Cos(azimuth), math.Cos(low)) / d
	v.Pitch = -math.Asin(math.Sin(low)*math.Sin(azimuth)) / d
	v.Roll = radialRoll(v)
	return v
}
