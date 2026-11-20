//go:build windows

package main

import (
	"context"
	"log"
	"syscall"
	"unsafe"
)

const (
	serviceWin32OwnProcess = 0x10
	serviceStartPending    = 2
	serviceStopPending     = 3
	serviceRunning         = 4
	serviceStopped         = 1
	serviceAcceptStop      = 1
	controlStop            = 1
	controllerNotConnected = syscall.Errno(1063)
)

type serviceTable struct {
	Name     *uint16
	Callback uintptr
}
type serviceStatus struct{ Type, State, Accepted, ExitCode, SpecificExitCode, Checkpoint, WaitHint uint32 }

var serviceDLL = syscall.NewLazyDLL("advapi32.dll")
var startDispatcher = serviceDLL.NewProc("StartServiceCtrlDispatcherW")
var registerControl = serviceDLL.NewProc("RegisterServiceCtrlHandlerW")
var setStatus = serviceDLL.NewProc("SetServiceStatus")
var scHandle uintptr
var scCancel context.CancelFunc
var scServer *Server

func status(st uint32) {
	accepted := uint32(0)
	if st == serviceRunning {
		accepted = serviceAcceptStop
	}
	state := serviceStatus{Type: serviceWin32OwnProcess, State: st, Accepted: accepted}
	setStatus.Call(scHandle, uintptr(unsafe.Pointer(&state)))
}
func controlHandler(ctrl uint32) uintptr {
	if ctrl == controlStop {
		status(serviceStopPending)
		if scCancel != nil {
			scCancel()
		}
	}
	return 0
}
func serviceMain(argc uint32, argv **uint16) uintptr {
	name, _ := syscall.UTF16PtrFromString("Spherelay")
	h, _, _ := registerControl.Call(uintptr(unsafe.Pointer(name)), syscall.NewCallback(controlHandler))
	if h == 0 {
		return 0
	}
	scHandle = h
	status(serviceStartPending)
	ctx, cancel := context.WithCancel(context.Background())
	scCancel = cancel
	status(serviceRunning)
	if e := scServer.run(ctx); e != nil {
		log.Println("service:", e)
	}
	cancel()
	status(serviceStopped)
	return 0
}
func runWindowsService(s *Server) bool {
	scServer = s
	name, _ := syscall.UTF16PtrFromString("Spherelay")
	entries := [2]serviceTable{{Name: name, Callback: syscall.NewCallback(serviceMain)}, {}}
	ok, _, e := startDispatcher.Call(uintptr(unsafe.Pointer(&entries[0])))
	if ok == 0 {
		if e == controllerNotConnected {
			return false
		}
		log.Println("Service dispatcher:", e)
		return false
	}
	return true
}
