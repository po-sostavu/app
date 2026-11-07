package release

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectInvalidRequest(t *testing.T) {
	valid := `{"schema":1,"sha":"` + strings.Repeat("a", 40) + `","run_id":1,"rollback_compatible":true,"images":{"pwa":"sha256:` + strings.Repeat("1", 64) + `","api":"sha256:` + strings.Repeat("1", 64) + `","migrate":"sha256:` + strings.Repeat("1", 64) + `","postgres":"sha256:` + strings.Repeat("1", 64) + `"}}`
	if _, _, err := ParseRequest([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"[]", "null", "{}", strings.Repeat("x", 4097), `{"schema":1,"schema":1}`, strings.Replace(valid, `"run_id":1`, `"run_id":true`, 1), strings.Replace(valid, `"rollback_compatible":true`, `"rollback_compatible":false`, 1), strings.Replace(valid, strings.Repeat("a", 40), "$(id)", 1), strings.Replace(valid, `"schema":1`, `"schema":1,"command":"id"`, 1), valid + valid} {
		if _, _, err := ParseRequest([]byte(input)); err == nil {
			t.Errorf("accepted invalid request: %.80s", input)
		}
	}
	rollback := `{"operation":"rollback","expected_sha":"` + strings.Repeat("b", 40) + `"}`
	if _, r, err := ParseRequest([]byte(rollback)); err != nil || r == nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestArtifactValidationBeforeLoad(t *testing.T) {
	directory := t.TempDir()
	archive := filepath.Join(directory, "image.tar.gz")
	if err := os.WriteFile(archive, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	metadata := Metadata{Image: "api", SHA: strings.Repeat("a", 40), ImageID: "sha256:" + strings.Repeat("1", 64), Bytes: 7, SHA256: "wrong"}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "metadata.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, sha := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		if _, err := VerifyArchive(path, sha); err == nil {
			t.Error("accepted invalid archive")
		}
	}
}

type fakeRegistry struct {
	config  string
	missing bool
}

func (f fakeRegistry) Manifest(string, string) (*Manifest, error) {
	if f.missing {
		return nil, nil
	}
	m := &Manifest{Digest: "sha256:" + strings.Repeat("d", 64)}
	m.Config.Digest = f.config
	return m, nil
}
func (f fakeRegistry) ConfigID(_ string, m *Manifest) (string, error) { return m.Config.Digest, nil }

func TestExistingTagConflictAndReuse(t *testing.T) {
	loaded := map[string]string{}
	for _, name := range Names {
		loaded[name] = "sha256:" + strings.Repeat("a", 64)
	}
	docker := func(...string) ([]byte, error) { t.Error("unexpected push for existing tag"); return nil, nil }
	output := filepath.Join(t.TempDir(), "release.json")
	if err := Publish(loaded, strings.Repeat("b", 40), 1, output, fakeRegistry{config: "different"}, docker); err == nil {
		t.Fatal("tag overwrite allowed")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed publication wrote manifest")
	}
	if err := Publish(loaded, strings.Repeat("b", 40), 1, output, fakeRegistry{config: loaded["api"]}, docker); err != nil {
		t.Fatal(err)
	}
}

func TestFailedPushDoesNotProduceRelease(t *testing.T) {
	loaded := map[string]string{}
	for _, name := range Names {
		loaded[name] = "sha256:" + strings.Repeat("a", 64)
	}
	output := filepath.Join(t.TempDir(), "release.json")
	err := Publish(loaded, strings.Repeat("b", 40), 1, output, fakeRegistry{missing: true}, func(args ...string) ([]byte, error) {
		if args[0] == "push" {
			return nil, errors.New("registry unavailable")
		}
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected publication failure")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("partial publication wrote manifest")
	}
}
