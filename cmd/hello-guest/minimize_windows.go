//go:build windows

package main

import "syscall"

// minimizeConsole сворачивает окно консоли в таскбар.
func minimizeConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd != 0 {
		user32.NewProc("ShowWindow").Call(hwnd, 6) // SW_MINIMIZE
	}
}
