//go:build !windows

package core

func WatchParentProcess(onExit func()) {}
