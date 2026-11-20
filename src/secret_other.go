//go:build !windows

package main

import "encoding/base64"

// Development-only encryption fallback: NOT suitable for deployment; production Windows uses DPAPI.
func protectSecret(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func unprotectSecret(s string) (string, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	return string(b), e
}
