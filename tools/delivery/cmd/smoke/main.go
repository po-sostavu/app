package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/po-sostavu/app/tools/delivery/internal/deploy"
	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

func random() string {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data[:])
}
func copyFile(source, target string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, raw, 0644)
}

func run(repository string) error {
	if runtime.GOOS != "linux" {
		return errors.New("integration requires an isolated Linux Docker runner")
	}
	prefix := "posostavu-ci-" + random()[:8]
	root, err := os.MkdirTemp("", prefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	files, secrets, routes := filepath.Join(root, "deploy"), filepath.Join(root, "secrets"), filepath.Join(root, "routes")
	for _, directory := range []string{files, secrets, routes} {
		if err := os.Mkdir(directory, 0755); err != nil {
			return err
		}
	}
	for _, name := range []string{"compose.application.yaml", "compose.database.yaml", "Caddyfile"} {
		if err := copyFile(filepath.Join(repository, "infra", "production", name), filepath.Join(files, name)); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(repository, "infra", "postgres", "init.sh"), filepath.Join(files, "init.sh")); err != nil {
		return err
	}
	admin, app, migrator := random()+"$literal", random()+"$literal", random()+"$literal"
	for name, content := range map[string]string{"postgres": "POSTGRES_PASSWORD=" + admin + "\nAPP_DB_PASSWORD=" + app + "\nMIGRATOR_DB_PASSWORD=" + migrator + "\n", "api": "DB_PASSWORD=" + app + "\n", "migrate": "DB_PASSWORD=" + migrator + "\n"} {
		if err := os.WriteFile(filepath.Join(secrets, name+".env"), []byte(content), 0600); err != nil {
			return err
		}
	}
	config := deploy.Config{TraefikDirectory: routes, EdgeNetwork: prefix + "-edge", Domain: "test.local", HTTPSEntrypoint: "web", HTTPEntrypoint: "unused", CertResolver: "unused", ObserveSeconds: 1, ReserveDiskMiB: 64}
	driver := &deploy.DockerRuntime{Root: root, Config: config, Prefix: prefix, LocalImages: true}
	first := release.Release{Schema: 1, SHA: strings.Repeat("1", 40), RunID: 1, RollbackCompatible: true, Images: map[string]string{}}
	for _, name := range release.Names {
		first.Images[name] = "sha256:" + strings.Repeat("1", 64)
	}
	second := first
	second.SHA = strings.Repeat("2", 40)
	second.RunID = 2
	traefik := prefix + "-traefik"
	if _, err := driver.Run(driver.Environment(), "network", "create", config.EdgeNetwork); err != nil {
		return err
	}
	defer func() {
		for _, color := range []string{"blue", "green", ""} {
			_, _ = driver.Compose(first, color, "down", "--volumes", "--remove-orphans")
		}
		_, _ = driver.Run(driver.Environment(), "rm", "--force", traefik)
		_, _ = driver.Run(driver.Environment(), "network", "rm", config.EdgeNetwork)
	}()
	if _, err := driver.Run(driver.Environment(), "run", "-d", "--name", traefik, "--network", config.EdgeNetwork, "--publish", "127.0.0.1::8080", "--mount", "type=bind,source="+routes+",target=/routes,readonly", os.Getenv("TRAEFIK_TEST_IMAGE"), "--entrypoints.web.address=:8080", "--providers.file.directory=/routes", "--providers.file.watch=true", "--log.level=ERROR"); err != nil {
		return err
	}
	raw, err := driver.Run(driver.Environment(), "inspect", traefik)
	if err != nil {
		return err
	}
	var info []struct {
		NetworkSettings struct {
			Ports map[string][]struct{ HostPort string }
		}
	}
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return err
	}
	driver.TestURL = "http://127.0.0.1:" + info[0].NetworkSettings.Ports["8080/tcp"][0].HostPort
	engine := deploy.Engine{Root: root, Route: driver.Route(), Runtime: driver}
	for _, color := range []string{"", "blue", "green"} {
		if _, err := driver.Compose(first, color, "config", "--quiet"); err != nil {
			return err
		}
	}
	if err := engine.Execute(&first, nil); err != nil {
		return err
	}
	databaseID, err := driver.Compose(first, "", "ps", "-q", "postgres")
	if err != nil {
		return err
	}
	if _, err := driver.Compose(first, "", "exec", "-T", "--user", "postgres", "postgres", "psql", "-d", "posostavu", "-v", "ON_ERROR_STOP=1", "-c", "CREATE TABLE cd_probe (value text); INSERT INTO cd_probe VALUES ('preserved');"); err != nil {
		return err
	}
	assets, err := filepath.Glob(filepath.Join(root, "assets", "assets", "*.js"))
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return errors.New("missing JavaScript assets")
	}
	assetPath := "/assets/" + filepath.Base(assets[0])
	original, err := driver.ReadAsset(assetPath)
	if err != nil {
		return err
	}
	if err := engine.Execute(&second, nil); err != nil {
		return err
	}
	currentID, err := driver.Compose(first, "", "ps", "-q", "postgres")
	if err != nil {
		return err
	}
	if currentID != databaseID {
		return errors.New("shared database recreated")
	}
	if err := engine.Execute(nil, &release.Rollback{Operation: "rollback", ExpectedSHA: second.SHA}); err != nil {
		return err
	}
	if err := driver.Verify(first); err != nil {
		return err
	}
	current, err := driver.ReadAsset(assetPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, current) {
		return errors.New("old PWA asset changed")
	}
	value, err := driver.Compose(first, "", "exec", "-T", "--user", "postgres", "postgres", "psql", "-d", "posostavu", "-Atc", "SELECT value FROM cd_probe")
	if err != nil {
		return err
	}
	if value != "preserved" {
		return errors.New("database contents changed")
	}
	backups, err := filepath.Glob(filepath.Join(root, "backups", "*.dump"))
	if err != nil {
		return err
	}
	if len(backups) != 2 {
		return errors.New("backup count mismatch")
	}
	fmt.Println("PASS: migrations, raw secrets, blue/green, Traefik switch, rollback, database and old assets")
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "smoke REPOSITORY")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
