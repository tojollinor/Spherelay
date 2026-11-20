package main

import "time"

type transferSample struct {
	at    time.Time
	bytes int64
}
type transferRate struct{ samples []transferSample }

// Use a short moving window of actual bytes, rather than rounded MB counters.
func (r *transferRate) sample(at time.Time, bytes int64) (float64, bool) {
	if len(r.samples) > 0 && (bytes < r.samples[len(r.samples)-1].bytes || !at.After(r.samples[len(r.samples)-1].at)) {
		r.samples = nil
	}
	r.samples = append(r.samples, transferSample{at, bytes})
	for len(r.samples) > 2 && at.Sub(r.samples[0].at) > 3*time.Second {
		r.samples = r.samples[1:]
	}
	first := r.samples[0]
	elapsed := at.Sub(first.at).Seconds()
	if elapsed <= 0 {
		return 0, false
	}
	return float64(bytes-first.bytes) / elapsed / (1 << 20), true
}
