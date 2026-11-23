//go:build !linux

package main

func isTerminal(uintptr) bool {
	return false
}

func terminalWidth(uintptr) int {
	return 80
}
