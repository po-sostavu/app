package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

type Previous struct {
	Release release.Release `json:"release"`
	Color   string          `json:"color"`
}

type State struct {
	Release      release.Release `json:"release"`
	Color        string          `json:"color"`
	Database     release.Release `json:"database"`
	HighestRunID int64           `json:"highest_run_id"`
	Previous     *Previous       `json:"previous"`
}

type Runtime interface {
	Preflight(bool) error
	Pull(release.Release) error
	Database(release.Release, bool) error
	Stop(release.Release, string) error
	Backup(release.Release, string) error
	Migrate(release.Release, string) error
	Assets(release.Release) error
	Start(release.Release, string) error
	Inspect(release.Release, string) error
	Switch(release.Release, string) error
	Verify(release.Release) error
}

type Engine struct {
	Root       string
	Route      string
	Runtime    Runtime
	writeState func(string, any) error
}

func (e Engine) Execute(candidate *release.Release, rollback *release.Rollback) (err error) {
	files := filepath.Join(e.Root, "deploy")
	journal := filepath.Join(files, "in-progress.json")
	statePath := filepath.Join(files, "state.json")
	if Exists(journal) {
		return errors.New("unfinished deployment requires administrator review")
	}
	var state *State
	if Exists(statePath) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return err
		}
		if err := release.StrictJSON(raw, &state); err != nil {
			return err
		}
		if state == nil || (state.Color != "blue" && state.Color != "green") {
			return errors.New("invalid stored state")
		}
	}
	var next release.Release
	color := "blue"
	if rollback != nil {
		if state == nil || state.Previous == nil || state.Release.SHA != rollback.ExpectedSHA {
			return errors.New("rollback target missing or active release changed")
		}
		next = state.Previous.Release
		color = state.Previous.Color
	} else {
		if candidate == nil {
			return errors.New("missing release")
		}
		next = *candidate
		if state != nil && reflect.DeepEqual(state.Release, next) {
			fmt.Println("Release already active")
			return nil
		}
		if state != nil && next.RunID <= state.HighestRunID {
			return errors.New("stale release rejected")
		}
		if state != nil && state.Color == "blue" {
			color = "green"
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if color != "blue" && color != "green" {
		return errors.New("invalid color")
	}
	if err := e.Runtime.Preflight(state == nil); err != nil {
		return err
	}
	for name, mode := range map[string]os.FileMode{filepath.Join(e.Root, "assets"): 0755, filepath.Join(e.Root, "backups"): 0700, filepath.Join(files, "docker-client"): 0700} {
		if err := os.MkdirAll(name, mode); err != nil {
			return err
		}
		if err := os.Chmod(name, mode); err != nil {
			return err
		}
	}
	phase := func(value string) error {
		return WriteJSON(journal, map[string]any{"release": next, "color": color, "phase": value})
	}
	if err := phase("preparation"); err != nil {
		return err
	}
	oldRoute, readErr := os.ReadFile(e.Route)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	migrationStarted, switchStarted, committed := false, false, false
	defer func() {
		if err == nil {
			return
		}
		if switchStarted && !committed {
			if readErr == nil {
				err = errors.Join(err, AtomicWrite(e.Route, oldRoute, 0644))
			} else if removeErr := os.Remove(e.Route); removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Join(err, removeErr)
			}
			if state != nil {
				err = errors.Join(err, e.Runtime.Verify(state.Release))
			}
		}
		if !migrationStarted && rollback == nil && !committed {
			if removeErr := os.Remove(journal); removeErr != nil && !os.IsNotExist(removeErr) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if err = e.Runtime.Pull(next); err != nil {
		return err
	}
	database := next
	if state != nil {
		database = state.Database
	}
	if err = e.Runtime.Database(database, state == nil); err != nil {
		return err
	}
	if err = e.Runtime.Stop(next, color); err != nil {
		return err
	}
	if rollback == nil {
		if err = e.Runtime.Backup(database, next.SHA); err != nil {
			return err
		}
		migrationStarted = true
		if err = phase("migration"); err != nil {
			return err
		}
		if err = e.Runtime.Migrate(next, color); err != nil {
			return err
		}
	}
	if err = e.Runtime.Assets(next); err != nil {
		return err
	}
	if err = e.Runtime.Start(next, color); err != nil {
		return err
	}
	if err = e.Runtime.Inspect(next, color); err != nil {
		return err
	}
	if err = phase("switch"); err != nil {
		return err
	}
	switchStarted = true
	if err = e.Runtime.Switch(next, color); err != nil {
		return err
	}
	updated := State{Release: next, Color: color, Database: database, HighestRunID: next.RunID}
	if state != nil {
		updated.Previous = &Previous{Release: state.Release, Color: state.Color}
		if state.HighestRunID > updated.HighestRunID {
			updated.HighestRunID = state.HighestRunID
		}
	}
	persist := e.writeState
	if persist == nil {
		persist = WriteJSON
	}
	if err = persist(statePath, updated); err != nil {
		return err
	}
	committed = true
	if err = os.Remove(journal); err != nil {
		return err
	}
	message, _ := json.Marshal(map[string]string{"sha": next.SHA, "color": color, "status": "active"})
	fmt.Println(string(message))
	if state != nil {
		return e.Runtime.Stop(state.Release, state.Color)
	}
	return nil
}
