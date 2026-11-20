//go:build !windows

package main

func runWindowsService(s *Server) bool { return false }
