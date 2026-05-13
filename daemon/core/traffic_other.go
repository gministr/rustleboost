//go:build !windows

package core

func readNetStats() (recv int64, sent int64, err error) {
	return 0, 0, nil
}
