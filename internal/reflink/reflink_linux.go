//go:build linux

package reflink

import (
	"os"
	"syscall"
)

// FICLONE is Linux' copy-on-write clone ioctl. It works on btrfs, XFS,
// newer ext4 and several other filesystems. A failed ioctl is deliberately
// reported to the caller so auto mode can fall back to a normal copy.
const ficlone = uintptr(0x40049409)

func Clone(src, dst string, mode os.FileMode) (bool, error) {
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return false, err
	}
	defer out.Close()
	r1, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficlone, in.Fd())
	if errno != 0 || r1 != 0 {
		return false, errno
	}
	return true, nil
}
