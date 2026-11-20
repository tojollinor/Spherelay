package main

import (
	"context"
	"os/exec"
)

func backgroundCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureBackgroundCommand(cmd)
	return cmd
}
func backgroundCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureBackgroundCommand(cmd)
	return cmd
}
