//go:build !windows

package main

func nativeWindow(url string) bool { return false }
