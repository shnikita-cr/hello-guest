//go:build darwin

package main

import "os/exec"

// minimizeConsole сворачивает окно терминала (Cmd+M фронтального окна —
// это окно терминала, пользователь только что нажал Enter именно здесь).
func minimizeConsole() {
	exec.Command("osascript", "-e",
		`tell application "System Events" to keystroke "m" using {command down}`).Run()
}
