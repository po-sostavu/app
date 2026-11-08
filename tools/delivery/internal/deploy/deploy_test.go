package deploy

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/po-sostavu/app/tools/delivery/internal/release"
)

func archive(t *testing.T, name, content string, link bool) *bytes.Reader {
	t.Helper()
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	header := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
	if link {
		header.Typeflag = tar.TypeSymlink
		header.Size = 0
		header.Linkname = "/etc/passwd"
	}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if !link {
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(out.Bytes())
}

func TestAssetsPreservedAndCollisionRejected(t *testing.T) {
	directory := t.TempDir()
	for name, content := range map[string]string{"assets/old.js": "old", "assets/new.js": "new", "workbox-123.js": "worker", "index.html": "index"} {
		if err := RetainAssets(archive(t, name, content, false), directory); err != nil {
			t.Fatal(err)
		}
	}
	if Exists(filepath.Join(directory, "index.html")) {
		t.Error("entrypoint retained in immutable storage")
	}
	if err := RetainAssets(archive(t, "assets/old.js", "changed", false), directory); err == nil {
		t.Error("immutable collision accepted")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "assets/old.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old" {
		t.Error("old asset changed")
	}
}

func TestRejectUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "assets/../../escape", "assets\\..\\escape"} {
		if err := RetainAssets(archive(t, name, "x", false), t.TempDir()); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if err := RetainAssets(archive(t, "assets/link", "", true), t.TempDir()); err == nil {
		t.Error("accepted symlink")
	}
}

func testRelease(number int) release.Release {
	r := release.Release{Schema: 1, SHA: fmt.Sprintf("%040x", number), RunID: int64(number), RollbackCompatible: true, Images: map[string]string{}}
	for _, name := range release.Names {
		r.Images[name] = "sha256:" + fmt.Sprintf("%064x", number)
	}
	return r
}

type fakeRuntime struct {
	root, route, failure string
	operations           []string
}

func (f *fakeRuntime) record(name string) error {
	f.operations = append(f.operations, name)
	if name == f.failure {
		return errors.New("injected " + name + " failure")
	}
	return nil
}
func (f *fakeRuntime) Preflight(bool) error       { return f.record("resources") }
func (f *fakeRuntime) Pull(release.Release) error { return f.record("pull") }
func (f *fakeRuntime) Database(_ release.Release, first bool) error {
	if first {
		return f.record("database-start")
	}
	return f.record("database-check")
}
func (f *fakeRuntime) Stop(_ release.Release, color string) error { return f.record("stop-" + color) }
func (f *fakeRuntime) Backup(release.Release, string) error       { return f.record("backup") }
func (f *fakeRuntime) Migrate(release.Release, string) error      { return f.record("migration") }
func (f *fakeRuntime) Assets(release.Release) error               { return f.record("assets") }
func (f *fakeRuntime) Start(release.Release, string) error        { return f.record("start") }
func (f *fakeRuntime) Inspect(release.Release, string) error      { return f.record("candidate") }
func (f *fakeRuntime) Verify(release.Release) error               { return f.record("restored-check") }
func (f *fakeRuntime) Switch(_ release.Release, color string) error {
	if err := AtomicWrite(f.route, []byte(color), 0644); err != nil {
		return err
	}
	return f.record("public")
}

func fixture(t *testing.T) (Engine, *fakeRuntime) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "deploy"), 0755); err != nil {
		t.Fatal(err)
	}
	f := &fakeRuntime{root: root, route: filepath.Join(root, "route.yml")}
	return Engine{Root: root, Route: f.route, Runtime: f}, f
}

func readState(t *testing.T, e Engine) State {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.Root, "deploy", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestDeployIdempotenceRollbackAndDatabase(t *testing.T) {
	e, f := fixture(t)
	first, second := testRelease(1), testRelease(2)
	if err := e.Execute(&first, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Execute(&second, nil); err != nil {
		t.Fatal(err)
	}
	state := readState(t, e)
	if state.Color != "green" || state.Database.SHA != first.SHA {
		t.Fatal("wrong active color or database")
	}
	count := len(f.operations)
	if err := e.Execute(&second, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.operations) != count {
		t.Fatal("idempotent retry changed containers")
	}
	if err := e.Execute(nil, &release.Rollback{Operation: "rollback", ExpectedSHA: second.SHA}); err != nil {
		t.Fatal(err)
	}
	state = readState(t, e)
	if state.Color != "blue" || state.HighestRunID != 2 {
		t.Fatal("wrong rollback state")
	}
	migrations, dbStarts := 0, 0
	for _, op := range f.operations {
		if op == "migration" {
			migrations++
		}
		if op == "database-start" {
			dbStarts++
		}
	}
	if migrations != 2 || dbStarts != 1 {
		t.Fatal("rollback migrated or recreated database")
	}
	if err := e.Execute(&second, nil); err == nil {
		t.Fatal("stale release accepted")
	}
}

func TestFailuresPreserveRouteAndState(t *testing.T) {
	for _, failure := range []string{"resources", "backup", "migration", "candidate", "public"} {
		t.Run(failure, func(t *testing.T) {
			e, f := fixture(t)
			first, second := testRelease(1), testRelease(2)
			if err := e.Execute(&first, nil); err != nil {
				t.Fatal(err)
			}
			f.failure = failure
			if err := e.Execute(&second, nil); err == nil {
				t.Fatal("expected failure")
			}
			raw, err := os.ReadFile(e.Route)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != "blue" || readState(t, e).Release.SHA != first.SHA {
				t.Fatal("active release changed")
			}
			if failure == "backup" {
				migrations := 0
				for _, operation := range f.operations {
					if operation == "migration" {
						migrations++
					}
				}
				if migrations != 1 {
					t.Fatal("migration started after backup failure")
				}
			}
			if failure != "resources" && failure != "backup" {
				third := testRelease(3)
				if err := e.Execute(&third, nil); err == nil || !strings.Contains(err.Error(), "unfinished") {
					t.Fatal("unfinished deployment did not block retry")
				}
			}
		})
	}
}

func TestLockRejectsSecondOwner(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux file locking")
	}
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if second, err := Lock(path); err == nil {
		second()
		t.Fatal("second deployment acquired lock")
	}
}

func TestStateWriteFailureRestoresRoute(t *testing.T) {
	e, _ := fixture(t)
	first, second := testRelease(1), testRelease(2)
	if err := e.Execute(&first, nil); err != nil {
		t.Fatal(err)
	}
	e.writeState = func(string, any) error { return errors.New("disk full") }
	if err := e.Execute(&second, nil); err == nil {
		t.Fatal("expected state write failure")
	}
	raw, err := os.ReadFile(e.Route)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "blue" || readState(t, e).Release.SHA != first.SHA {
		t.Fatal("failed commit changed active release")
	}
	if !Exists(filepath.Join(e.Root, "deploy", "in-progress.json")) {
		t.Fatal("lost recovery journal")
	}
}
