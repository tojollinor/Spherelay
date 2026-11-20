//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	Size uint32
	Data *byte
}

func blob(b []byte) dataBlob {
	v := dataBlob{Size: uint32(len(b))}
	if len(b) > 0 {
		v.Data = &b[0]
	}
	return v
}

var cryptDLL = syscall.NewLazyDLL("crypt32.dll")
var protect = cryptDLL.NewProc("CryptProtectData")
var unprotect = cryptDLL.NewProc("CryptUnprotectData")
var localFree = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")

func dpapi(b []byte, encrypt bool) ([]byte, error) {
	input := blob(b)
	var out dataBlob
	fn := unprotect
	if encrypt {
		fn = protect
	}
	r, _, e := fn.Call(uintptr(unsafe.Pointer(&input)), 0, 0, 0, 0, func() uintptr {
		if encrypt {
			return 5
		}
		return 1
	}(), uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, fmt.Errorf("DPAPI: %w", e)
	}
	defer localFree.Call(uintptr(unsafe.Pointer(out.Data)))
	return append([]byte{}, unsafe.Slice(out.Data, int(out.Size))...), nil
}
func protectSecret(s string) (string, error) {
	b, e := dpapi([]byte(s), true)
	return base64.StdEncoding.EncodeToString(b), e
}
func unprotectSecret(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	clear, e := dpapi(b, false)
	return string(clear), e
}
