package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected export, load, publish or check-public")
	}
	switch args[0] {
	case "export":
		if len(args) != 4 {
			return errors.New("export IMAGE SHA DIRECTORY")
		}
		return release.Export(args[1], args[2], args[3])
	case "load":
		if len(args) != 3 {
			return errors.New("load DIRECTORY SHA")
		}
		_, err := release.Load(args[1], args[2])
		return err
	case "publish":
		if len(args) != 6 {
			return errors.New("publish DIRECTORY SHA RUN_ID OUTPUT POLICY")
		}
		raw, err := os.ReadFile(args[5])
		if err != nil {
			return err
		}
		var policy struct {
			RollbackCompatible bool   `json:"rollback_compatible"`
			Scope              string `json:"scope"`
		}
		if err := release.StrictJSON(raw, &policy); err != nil {
			return err
		}
		if !policy.RollbackCompatible {
			return errors.New("automatic deployment requires compatible rollback")
		}
		runID, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil || runID <= 0 {
			return errors.New("invalid CI run ID")
		}
		loaded, err := release.Load(args[1], args[2])
		if err != nil {
			return err
		}
		return release.Publish(loaded, args[2], runID, args[4], release.RegistryClient{Authenticated: true}, release.Docker)
	case "check-public":
		if len(args) != 2 {
			return errors.New("check-public RELEASE")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var r release.Release
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		for _, name := range release.Names {
			manifest, err := (release.RegistryClient{}).Manifest(name, r.Images[name])
			if err != nil {
				return err
			}
			if manifest == nil {
				return errors.New("image is not publicly available")
			}
		}
		return nil
	default:
		return errors.New("unknown release command")
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
