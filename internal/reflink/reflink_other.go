//go:build !linux

package reflink

import "os"

func Clone(src, dst string, mode os.FileMode) (bool, error) {
	return false, os.ErrInvalid
}
