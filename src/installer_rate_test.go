package main

import (
	"math"
	"testing"
	"time"
)

func TestInstallerTransferRate(t *testing.T) {
	var r transferRate
	start := time.Unix(1000, 0)
	if _, ok := r.sample(start, 0); ok {
		t.Fatal("speed claimed without elapsed time")
	}
	speed, ok := r.sample(start.Add(time.Second), 1<<20)
	if !ok || math.Abs(speed-1) > 0.001 {
		t.Fatal("wrong byte rate")
	}
	speed, _ = r.sample(start.Add(2*time.Second), 1<<20)
	if math.Abs(speed-.5) > 0.001 {
		t.Fatal("stall not reflected")
	}
	r.sample(start.Add(3*time.Second), 1<<20)
	speed, _ = r.sample(start.Add(6*time.Second), 1<<20)
	if speed != 0 {
		t.Fatal("stalled transfer retains old speed")
	}
	if _, ok := r.sample(start.Add(7*time.Second), 0); ok {
		t.Fatal("new transfer did not reset")
	}
}
