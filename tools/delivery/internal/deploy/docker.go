package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

type DockerRuntime struct {
	Root   string
	Config Config
	Prefix string
	// Used only by the isolated integration fixture, never set from server input.
	LocalImages bool
	TestURL     string
}

func (d *DockerRuntime) Files() string { return filepath.Join(d.Root, "deploy") }
func (d *DockerRuntime) Route() string {
	return filepath.Join(d.Config.TraefikDirectory, d.Prefix+".yml")
}
func (d *DockerRuntime) Image(r release.Release, name string) string {
	if d.LocalImages {
		return "posostavu-" + name + ":ci"
	}
	return release.Registry + name + "@" + r.Images[name]
}

func (d *DockerRuntime) Environment() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8", "DOCKER_CONFIG=" + filepath.Join(d.Files(), "docker-client"), "COMPOSE_DISABLE_ENV_FILE=1"}
}

func (d *DockerRuntime) Env(r release.Release, color string) []string {
	env := append(d.Environment(), "DEPLOY_SECRETS="+filepath.Join(d.Root, "secrets"), "DATABASE_NETWORK="+d.Prefix+"-database", "EDGE_NETWORK="+d.Config.EdgeNetwork, "ASSET_DIRECTORY="+filepath.Join(d.Root, "assets"), "RELEASE_SHA="+r.SHA, "GATEWAY_ALIAS="+d.Prefix+"-"+color+"-gateway")
	for _, name := range release.Names {
		env = append(env, strings.ToUpper(name)+"_IMAGE="+d.Image(r, name))
	}
	return env
}

func (d *DockerRuntime) Run(env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/docker", args...)
	command.Env = env
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("docker operation failed: %s", args[0])
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *DockerRuntime) ComposeArgs(color string) []string {
	kind, project := "application", color
	if color == "" {
		kind, project = "database", "db"
	}
	return []string{"compose", "--project-name", d.Prefix + "-" + project, "--file", filepath.Join(d.Files(), "compose."+kind+".yaml")}
}

func (d *DockerRuntime) Compose(r release.Release, color string, args ...string) (string, error) {
	return d.Run(d.Env(r, color), append(d.ComposeArgs(color), args...)...)
}

func (d *DockerRuntime) Preflight(first bool) error {
	if !d.LocalImages {
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return err
		}
		var available int64
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				if _, err := fmt.Sscanf(line, "MemAvailable: %d kB", &available); err != nil {
					return err
				}
			}
		}
		required := 512 + d.Config.ReserveMemoryMiB
		if first {
			required += 512
		}
		if available/1024 < int64(required) {
			return errors.New("insufficient memory for two colors")
		}
		for _, location := range []string{d.Root, "/var/lib/docker"} {
			free, err := FreeSpace(location)
			if err != nil {
				return err
			}
			if free < uint64(d.Config.ReserveDiskMiB)*1024*1024 {
				return errors.New("insufficient disk space")
			}
		}
	}
	_, err := d.Run(d.Environment(), "network", "inspect", d.Config.EdgeNetwork)
	return err
}

func (d *DockerRuntime) Pull(r release.Release) error {
	for _, name := range release.Names {
		image := d.Image(r, name)
		if !d.LocalImages {
			if _, err := d.Run(d.Environment(), "pull", image); err != nil {
				return err
			}
		}
		raw, err := d.Run(d.Environment(), "image", "inspect", image)
		if err != nil {
			return err
		}
		var images []release.ImageInfo
		if err := json.Unmarshal([]byte(raw), &images); err != nil {
			return err
		}
		if len(images) != 1 || images[0].OS != "linux" || images[0].Architecture != "amd64" {
			return errors.New("unsupported image platform")
		}
		if !d.LocalImages && (images[0].Config.Labels["org.opencontainers.image.revision"] != r.SHA || images[0].Config.Labels["org.opencontainers.image.source"] != release.Source) {
			return errors.New("image provenance mismatch")
		}
	}
	return nil
}

func (d *DockerRuntime) Database(r release.Release, first bool) error {
	if first {
		_, err := d.Compose(r, "", "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "postgres")
		return err
	}
	_, err := d.Compose(r, "", "exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "posostavu")
	return err
}

func (d *DockerRuntime) Stop(r release.Release, color string) error {
	_, err := d.Compose(r, color, "stop", "gateway", "api")
	return err
}
func (d *DockerRuntime) Migrate(r release.Release, color string) error {
	_, err := d.Compose(r, color, "run", "--rm", "--no-deps", "migrate", "up")
	return err
}
func (d *DockerRuntime) Start(r release.Release, color string) error {
	_, err := d.Compose(r, color, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120", "api", "gateway")
	return err
}

func (d *DockerRuntime) Backup(r release.Release, sha string) error {
	sizeText, err := d.Compose(r, "", "exec", "-T", "--user", "postgres", "postgres", "psql", "-d", "posostavu", "-Atc", "SELECT pg_database_size('posostavu')")
	if err != nil {
		return err
	}
	size, err := strconv.ParseUint(sizeText, 10, 64)
	if err != nil {
		return err
	}
	free, err := FreeSpace(d.Root)
	if err != nil {
		return err
	}
	if free <= size*2+uint64(d.Config.ReserveDiskMiB)*1024*1024 {
		return errors.New("insufficient backup space")
	}
	target := filepath.Join(d.Root, "backups", fmt.Sprintf("%d-%s.dump", time.Now().UnixNano(), sha))
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := append(d.ComposeArgs(""), "exec", "-T", "--user", "postgres", "postgres", "pg_dump", "-Fc", "-d", "posostavu")
	command := exec.CommandContext(ctx, "/usr/bin/docker", args...)
	command.Env = d.Env(r, "")
	command.Stdout = file
	err = command.Run()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("database backup failed")
	}
	file, err = os.Open(target)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	args = append(d.ComposeArgs(""), "exec", "-T", "--user", "postgres", "postgres", "pg_restore", "--list")
	command = exec.CommandContext(ctx, "/usr/bin/docker", args...)
	command.Env = d.Env(r, "")
	command.Stdin = file
	if err := command.Run(); err != nil {
		return errors.New("backup validation failed")
	}
	return nil
}

func (d *DockerRuntime) Assets(r release.Release) (err error) {
	container, err := d.Run(d.Environment(), "create", "--network", "none", d.Image(r, "pwa"))
	if err != nil {
		return err
	}
	defer func() { _, removeErr := d.Run(d.Environment(), "rm", container); err = errors.Join(err, removeErr) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/docker", "cp", container+":/srv/.", "-")
	command.Env = d.Environment()
	reader, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	err = RetainAssets(reader, filepath.Join(d.Root, "assets"))
	if err != nil {
		cancel()
	}
	waitErr := command.Wait()
	return errors.Join(err, waitErr)
}

func (d *DockerRuntime) Inspect(r release.Release, color string) error {
	for _, path := range []string{"/healthz", "/api/readyz", "/", "/manifest.webmanifest", "/sw.js"} {
		if _, err := d.Compose(r, color, "exec", "-T", "gateway", "wget", "-q", "-T", "10", "-O", "/dev/null", "http://127.0.0.1:8080"+path); err != nil {
			return err
		}
	}
	return nil
}

func (d *DockerRuntime) RouteDocument(color string) map[string]any {
	name := d.Prefix
	rule := "Host(`" + d.Config.Domain + "`)"
	router := map[string]any{"rule": rule, "entryPoints": []string{d.Config.HTTPSEntrypoint}, "service": name, "tls": map[string]string{"certResolver": d.Config.CertResolver}}
	routers := map[string]any{name: router, name + "-http": map[string]any{"rule": rule, "entryPoints": []string{d.Config.HTTPEntrypoint}, "service": name, "middlewares": []string{name + "-https"}}}
	if d.TestURL != "" {
		delete(router, "tls")
		delete(routers, name+"-http")
	}
	return map[string]any{"http": map[string]any{
		"routers":     routers,
		"middlewares": map[string]any{name + "-https": map[string]any{"redirectScheme": map[string]any{"scheme": "https", "permanent": true}}},
		"services":    map[string]any{name: map[string]any{"loadBalancer": map[string]any{"servers": []map[string]string{{"url": "http://" + name + "-" + color + "-gateway:8080"}}}}},
	}}
}

func (d *DockerRuntime) Verify(r release.Release) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("unexpected healthcheck redirect") }}
	base := "https://" + d.Config.Domain
	if d.TestURL != "" {
		base = d.TestURL
	}
	for _, path := range []string{"/healthz", "/api/readyz", "/"} {
		request, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Cache-Control", "no-cache")
		if d.TestURL != "" {
			request.Host = d.Config.Domain
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024*1024))
		_ = response.Body.Close()
		if copyErr != nil {
			return copyErr
		}
		if response.StatusCode != 200 {
			return errors.New("public healthcheck failed")
		}
		if path == "/healthz" && response.Header.Get("X-Release-Sha") != r.SHA {
			return errors.New("route has not switched to expected release")
		}
	}
	return nil
}

func (d *DockerRuntime) Switch(r release.Release, color string) error {
	raw, err := json.Marshal(d.RouteDocument(color))
	if err != nil {
		return err
	}
	if err := AtomicWrite(d.Route(), raw, 0644); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = d.Verify(r)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
	deadline = time.Now().Add(time.Duration(d.Config.ObserveSeconds) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if err := d.Verify(r); err != nil {
			return err
		}
	}
	return nil
}

// ReadAsset is used by the isolated integration test through the external proxy.
func (d *DockerRuntime) ReadAsset(path string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, d.TestURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Host = d.Config.Domain
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, errors.New("asset unavailable")
	}
	var output bytes.Buffer
	_, err = io.Copy(&output, io.LimitReader(response.Body, 16*1024*1024))
	return output.Bytes(), err
}
