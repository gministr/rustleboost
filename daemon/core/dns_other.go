//go:build !windows

package core

// SystemDNSServers has no implementation outside Windows; the generator falls
// back to fixed resolvers when this returns nothing.
func SystemDNSServers() []string { return nil }
