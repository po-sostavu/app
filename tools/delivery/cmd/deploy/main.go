package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/po-sostavu/app/tools/delivery/internal/deploy"
	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

func run() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || len(os.Args) != 1 {
		return errors.New("use the root-owned Linux forced command without arguments")
	}
	type result struct {
		raw []byte
		err error
	}
	channel := make(chan result, 1)
	go func() { raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4097)); channel <- result{raw, err} }()
	var input result
	select {
	case input = <-channel:
	case <-time.After(15 * time.Second):
		return errors.New("request read timeout")
	}
	if input.err != nil {
		return input.err
	}
	candidate, rollback, err := release.ParseRequest(input.raw)
	if err != nil {
		return err
	}
	config, err := deploy.LoadConfig(deploy.Root)
	if err != nil {
		return err
	}
	unlock, err := deploy.Lock(filepath.Join(deploy.Root, "deploy", "deployment.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	driver := &deploy.DockerRuntime{Root: deploy.Root, Config: config, Prefix: "posostavu"}
	engine := deploy.Engine{Root: deploy.Root, Route: driver.Route(), Runtime: driver}
	return engine.Execute(candidate, rollback)
}

func main() {
	if err := run(); err != nil {
		// Do not forward Docker output or environment values to the SSH client.
		fmt.Fprintln(os.Stderr, "Deployment stopped. Check root-owned state and server logs before retrying.")
		os.Exit(1)
	}
}
