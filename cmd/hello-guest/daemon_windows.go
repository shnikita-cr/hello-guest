//go:build windows

package main

import "os/exec"

func daemonize(cmd *exec.Cmd) {}
