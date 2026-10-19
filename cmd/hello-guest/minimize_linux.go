//go:build linux

package main

import "os/exec"

// minimizeConsole сворачивает активное окно через xdotool, если установлен.
func minimizeConsole() {
	if _, err := exec.LookPath("xdotool"); err == nil {
		exec.Command("xdotool", "windowminimize", "getactivewindow").Run()
	}
}
