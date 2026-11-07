//go:build linux

package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func Trusted(name string, private bool) error {
	name, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for current := name; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("deployment path is not root-controlled")
		}
		if current == name && private && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
			return errors.New("secret file must have mode 600")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func syncDirectory(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func Lock(name string) (func(), error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

func FreeSpace(name string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(name, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
