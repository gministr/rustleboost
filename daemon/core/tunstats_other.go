//go:build !windows

package core

func adapterCounters() (TrafficStats, bool) { return TrafficStats{}, false }
