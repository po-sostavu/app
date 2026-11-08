package release

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const MaxArchive = 512 * 1024 * 1024

type Metadata struct {
	Image         string  `json:"image"`
	SHA           string  `json:"sha"`
	ImageID       string  `json:"image_id"`
	SHA256        string  `json:"sha256"`
	Bytes         int64   `json:"bytes"`
	ExportSeconds float64 `json:"export_seconds"`
}

type ImageInfo struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string
	Config       struct{ Labels map[string]string }
}

func Docker(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s failed: %w", args[0], err)
	}
	return output, nil
}

func Inspect(tag, sha string) (ImageInfo, error) {
	var info []ImageInfo
	raw, err := Docker("image", "inspect", tag)
	if err != nil {
		return ImageInfo{}, err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return ImageInfo{}, err
	}
	if len(info) != 1 {
		return ImageInfo{}, errors.New("expected one image")
	}
	i := info[0]
	if i.OS != "linux" || i.Architecture != "amd64" || i.Config.Labels["org.opencontainers.image.source"] != Source || i.Config.Labels["org.opencontainers.image.revision"] != sha {
		return ImageInfo{}, errors.New("image platform or provenance mismatch")
	}
	return i, nil
}

func Checksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Export(name, sha, directory string) error {
	if !ValidName(name) || !ValidSHA(sha) {
		return errors.New("invalid image or revision")
	}
	tag := "posostavu-" + name + ":ci"
	info, err := Inspect(tag, sha)
	if err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0755); err != nil {
		return err
	}
	started := time.Now()
	archive := filepath.Join(directory, "image.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		return err
	}
	writer, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		_ = file.Close()
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "image", "save", tag)
	command.Stdout = writer
	err = command.Run()
	compressErr := writer.Close()
	closeErr := file.Close()
	if err != nil || compressErr != nil || closeErr != nil {
		return errors.Join(err, compressErr, closeErr)
	}
	stat, err := os.Stat(archive)
	if err != nil {
		return err
	}
	if stat.Size() > MaxArchive {
		return errors.New("image artifact exceeds 512 MiB")
	}
	sum, err := Checksum(archive)
	if err != nil {
		return err
	}
	metadata := Metadata{name, sha, info.ID, sum, stat.Size(), time.Since(started).Seconds()}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return os.WriteFile(filepath.Join(directory, "metadata.json"), append(raw, '\n'), 0644)
}

func VerifyArchive(path, sha string) (Metadata, error) {
	var data Metadata
	raw, err := os.ReadFile(path)
	if err != nil {
		return data, err
	}
	if len(raw) > 4096 {
		return data, errors.New("metadata too large")
	}
	if err := StrictJSON(raw, &data); err != nil {
		return data, err
	}
	if !ValidSHA(sha) || data.SHA != sha || !ValidName(data.Image) || !ValidDigest(data.ImageID) {
		return data, errors.New("invalid artifact provenance")
	}
	archive := filepath.Join(filepath.Dir(path), "image.tar.gz")
	stat, err := os.Stat(archive)
	if err != nil {
		return data, err
	}
	if stat.Size() <= 0 || stat.Size() != data.Bytes || stat.Size() > MaxArchive {
		return data, errors.New("archive size mismatch")
	}
	sum, err := Checksum(archive)
	if err != nil {
		return data, err
	}
	if sum != data.SHA256 {
		return data, errors.New("archive checksum mismatch")
	}
	return data, nil
}

func Load(directory, sha string) (map[string]string, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*", "metadata.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) != 4 {
		return nil, errors.New("expected four image artifacts")
	}
	result := map[string]string{}
	for _, path := range paths {
		data, err := VerifyArchive(path, sha)
		if err != nil {
			return nil, err
		}
		if _, ok := result[data.Image]; ok {
			return nil, errors.New("duplicate image")
		}
		if _, err := Docker("image", "load", "--input", filepath.Join(filepath.Dir(path), "image.tar.gz")); err != nil {
			return nil, err
		}
		info, err := Inspect("posostavu-"+data.Image+":ci", sha)
		if err != nil {
			return nil, err
		}
		if info.ID != data.ImageID {
			return nil, errors.New("image ID mismatch")
		}
		result[data.Image] = info.ID
	}
	return result, nil
}
