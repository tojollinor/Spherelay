//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var moveFile = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(src, dst string) error {
	a, e := syscall.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	b, e := syscall.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	ok, _, err := moveFile.Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), uintptr(0x1|0x8))
	if ok == 0 {
		return err
	}
	return nil
}
