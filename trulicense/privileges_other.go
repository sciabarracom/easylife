//go:build !linux

package main

// dropToRoot is a no-op where setuid binaries do not apply.
func dropToRoot() error { return nil }
