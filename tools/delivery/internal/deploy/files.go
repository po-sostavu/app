package deploy

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

func AtomicWrite(name string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(name), ".pending-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary, mode); err != nil {
		return err
	}
	if err := os.Rename(temporary, name); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(name))
}

func WriteJSON(name string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(name, append(raw, '\n'), 0600)
}

func Exists(name string) bool { _, err := os.Lstat(name); return err == nil }

func noLinks(name string) error {
	for current := name; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in asset path")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func fileHash(name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

var assetPattern = regexp.MustCompile(`^[a-zA-Z0-9_@+./-]+$`)
var workboxPattern = regexp.MustCompile(`^workbox-[a-zA-Z0-9_-]+\.js$`)

func RetainAssets(input io.Reader, directory string) error {
	archive := tar.NewReader(input)
	var total int64
	count := 0
	for {
		member, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(member.Name, "./")
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return errors.New("unsafe archive path")
		}
		for _, part := range strings.Split(name, "/") {
			if part == ".." {
				return errors.New("unsafe archive path")
			}
		}
		if member.Typeflag == tar.TypeDir {
			continue
		}
		if member.Typeflag != tar.TypeReg {
			return errors.New("archive links and special files forbidden")
		}
		total += member.Size
		count++
		if total > 256*1024*1024 || count > 10000 {
			return errors.New("static archive exceeds limits")
		}
		if !strings.HasPrefix(name, "assets/") && !workboxPattern.MatchString(name) {
			continue
		}
		if !assetPattern.MatchString(name) {
			return errors.New("invalid asset filename")
		}
		target := filepath.Join(directory, filepath.FromSlash(path.Clean(name)))
		if err := noLinks(target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		for parent := filepath.Dir(target); parent != filepath.Dir(directory); parent = filepath.Dir(parent) {
			if err := os.Chmod(parent, 0755); err != nil {
				return err
			}
		}
		if err := retainFile(archive, target, member.Size); err != nil {
			return err
		}
	}
	if count == 0 {
		return errors.New("empty static archive")
	}
	return nil
}

func retainFile(input io.Reader, target string, size int64) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".asset-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	n, copyErr := io.Copy(file, input)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if n != size {
		return errors.New("truncated static file")
	}
	if Exists(target) {
		old, err := fileHash(target)
		if err != nil {
			return err
		}
		current, err := fileHash(temporary)
		if err != nil {
			return err
		}
		if !bytes.Equal(old, current) {
			return errors.New("immutable asset collision")
		}
		return nil
	}
	if err := os.Chmod(temporary, 0644); err != nil {
		return err
	}
	return os.Rename(temporary, target)
}
