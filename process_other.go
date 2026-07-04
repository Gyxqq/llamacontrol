//go:build !windows

package main

import "os/exec"

func configureHiddenCommandWindow(cmd *exec.Cmd) {}
