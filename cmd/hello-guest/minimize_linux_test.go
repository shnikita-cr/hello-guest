//go:build linux

package main

import "testing"

// Без xdotool — безопасный no-op; с xdotool в headless-окружении вызов безвреден.
func TestMinimizeConsoleNoop(t *testing.T) { minimizeConsole() }
