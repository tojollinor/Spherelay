package main

import (
	"os"
	"testing"
	"time"
)

func TestShutdownFixture(t *testing.T) {
	if os.Getenv("SPHERELAY_SHUTDOWN_FIXTURE") == "1" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}
func TestShutdownWaitsForVideoProcess(t *testing.T) {
	s, e := newServer(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cmd := backgroundCommand(os.Args[0], "-test.run=^TestShutdownFixture$")
	cmd.Env = append(os.Environ(), "SPHERELAY_SHUTDOWN_FIXTURE=1")
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	s.media = &Process{Cmd: cmd, Done: done}
	go func() { _ = cmd.Wait(); close(done) }()
	s.shutdown()
	select {
	case <-done:
	default:
		t.Fatal("shutdown returned before child process was reaped")
	}
	if s.media != nil {
		t.Fatal("video process retained after shutdown")
	}
}
