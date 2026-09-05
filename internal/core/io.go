package core

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/iruanp/aiwr/internal/reflink"
)

const (
	DefaultMaxInputBytes = int64(256 << 20)
	DefaultMaxStdinBytes = int64(64 << 20)
	DefaultMaxZipBytes   = int64(128 << 20)
)

func maxInputBytes() int64 {
	if v := os.Getenv("WATERMARKS_MAX_INPUT_BYTES"); v != "" {
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxInputBytes
}

// MaxInputBytes returns the configured whole-input cap. Keep this accessor
// exported so command-line integrations use the same environment override as
// the core file and HTTP paths.
func MaxInputBytes() int64 {
	return maxInputBytes()
}

func maxStdinBytes() int64 {
	if v := os.Getenv("WATERMARKS_MAX_STDIN_BYTES"); v != "" {
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxStdinBytes
}

// MaxStdinBytes returns the configured cap for stdin-backed text commands.
func MaxStdinBytes() int64 {
	return maxStdinBytes()
}

func readRegular(path string) ([]byte, os.FileMode, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("refusing non-regular input: %s", path)
	}
	if st.Size() > maxInputBytes() {
		return nil, 0, fmt.Errorf("refusing input larger than %d bytes: %s", maxInputBytes(), path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes()+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(data)) > maxInputBytes() {
		return nil, 0, fmt.Errorf("refusing input larger than %d bytes: %s", maxInputBytes(), path)
	}
	return data, st.Mode().Perm(), nil
}

func ensureNoSymlink(path string) error {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink: %s", path)
	}
	return nil
}

func ensureNoSymlinkComponents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(os.PathSeparator)
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(abs, volume), string(os.PathSeparator)), string(os.PathSeparator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		st, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return statErr
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink directory: %s", current)
		}
		if !st.IsDir() {
			return fmt.Errorf("output path component is not a directory: %s", current)
		}
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := ensureNoSymlink(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := ensureNoSymlinkComponents(parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func copyFile(src, dst string, mode os.FileMode, reflinkMode ReflinkMode) (bool, error) {
	if err := ensureNoSymlink(dst); err != nil {
		return false, err
	}
	if err := ensureNoSymlinkComponents(filepath.Dir(dst)); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if reflinkMode != ReflinkNever {
		if ok, err := reflink.Clone(src, dst, mode); ok {
			return true, nil
		} else if reflinkMode == ReflinkAlways {
			_ = os.Remove(dst)
			return false, fmt.Errorf("reflink clone %s -> %s failed: %w", src, dst, err)
		} else {
			_ = os.Remove(dst)
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return false, err
	}
	_, copyErr := io.Copy(out, in)
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return false, copyErr
}

func backupFile(path string, reflinkMode ReflinkMode) (string, bool, error) {
	bak := path + ".bak"
	if err := ensureNoSymlinkComponents(filepath.Dir(bak)); err != nil {
		return bak, false, err
	}
	if _, err := os.Lstat(bak); err == nil {
		st, statErr := os.Lstat(bak)
		if statErr != nil {
			return bak, false, fmt.Errorf("cannot inspect backup %s", bak)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			// Preserve a pre-existing symlink without following or replacing it.
			// The source write still goes through writeAtomic's own destination
			// checks, so the link cannot redirect the in-place clean.
			return bak, false, nil
		}
		if !st.Mode().IsRegular() {
			return bak, false, fmt.Errorf("cannot use backup %s", bak)
		}
		return bak, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return bak, false, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return bak, false, err
	}
	// Build the copy under a private name, then install it with a hard link.
	// Link is intentionally used instead of Rename because Rename replaces an
	// existing destination on Unix and could race with another cleaner.
	tmp, err := os.CreateTemp(filepath.Dir(bak), "."+filepath.Base(bak)+".*.tmp")
	if err != nil {
		return bak, false, err
	}
	tmpName := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpName)
		return bak, false, closeErr
	}
	_ = os.Remove(tmpName)
	_, copyErr := copyFile(path, tmpName, st.Mode().Perm(), reflinkMode)
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return bak, false, copyErr
	}
	if err := os.Link(tmpName, bak); err != nil {
		_ = os.Remove(tmpName)
		if errors.Is(err, os.ErrExist) {
			return bak, false, nil
		}
		// Hard links are unavailable on some filesystems/platforms. Fall back
		// to an exclusive byte copy rather than renaming over a backup that may
		// have appeared concurrently.
		if copyErr := copyFileExclusive(path, bak, st.Mode().Perm()); copyErr == nil {
			return bak, true, nil
		} else if errors.Is(copyErr, os.ErrExist) {
			return bak, false, nil
		} else {
			return bak, false, fmt.Errorf("create backup link (%v) and copy backup: %w", err, copyErr)
		}
	}
	_ = os.Remove(tmpName)
	return bak, true, nil
}

func copyFileExclusive(src, dst string, mode os.FileMode) error {
	if err := ensureNoSymlink(dst); err != nil {
		return err
	}
	if err := ensureNoSymlinkComponents(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(dst)
	}
	return copyErr
}

func cleanedPath(src string) string {
	ext := filepath.Ext(src)
	if ext == "" {
		return src + ".cleaned"
	}
	base := strings.TrimSuffix(src, ext)
	return base + ".cleaned" + ext
}

func directoryCleanedPath(src string) string { return src + ".cleaned" }

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	// The digest is a cheap reject for large unequal buffers; bytes.Equal is
	// retained as the final collision-safe equality check.
	h1, h2 := sha256.Sum256(a), sha256.Sum256(b)
	return h1 == h2 && bytes.Equal(a, b)
}

// WriteFileAtomic exposes the same symlink-safe atomic writer used by the
// file cleaner to the CLI's optional rewrite path.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	return writeAtomic(path, data, mode)
}

// BackupFile creates (or preserves) the sidecar backup used by in-place
// operations. It intentionally does not replace an existing regular backup.
func BackupFile(path string, reflinkMode ReflinkMode) (string, bool, error) {
	return backupFile(path, reflinkMode)
}
