package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

var Names = []string{"pwa", "api", "migrate", "postgres"}

const Source = "https://github.com/po-sostavu/app"
const Registry = "ghcr.io/po-sostavu/app-"

var shaPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Release struct {
	Schema             int               `json:"schema"`
	SHA                string            `json:"sha"`
	RunID              int64             `json:"run_id"`
	Images             map[string]string `json:"images"`
	RollbackCompatible bool              `json:"rollback_compatible"`
}

type Rollback struct {
	Operation   string `json:"operation"`
	ExpectedSHA string `json:"expected_sha"`
}

func ValidSHA(value string) bool    { return shaPattern.MatchString(value) }
func ValidDigest(value string) bool { return digestPattern.MatchString(value) }
func ValidName(value string) bool {
	for _, name := range Names {
		if value == name {
			return true
		}
	}
	return false
}

func (r Release) Validate() error {
	if r.Schema != 1 || !ValidSHA(r.SHA) || r.RunID <= 0 || r.RunID >= 1e15 || !r.RollbackCompatible || len(r.Images) != 4 {
		return errors.New("invalid release or unconfirmed rollback compatibility")
	}
	for _, name := range Names {
		if !ValidDigest(r.Images[name]) {
			return errors.New("expected four valid image digests")
		}
	}
	return nil
}

func StrictJSON(raw []byte, value any) error {
	// encoding/json otherwise accepts duplicate keys. Reject them at every depth.
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("JSON nesting too deep")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate JSON field")
			}
			seen[name] = true
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func ParseRequest(raw []byte) (*Release, *Rollback, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return nil, nil, errors.New("request exceeds 4096 bytes")
	}
	var fields map[string]json.RawMessage
	if err := StrictJSON(raw, &fields); err != nil {
		return nil, nil, err
	}
	if _, ok := fields["operation"]; ok {
		var r Rollback
		if err := StrictJSON(raw, &r); err != nil {
			return nil, nil, err
		}
		if r.Operation != "rollback" || !ValidSHA(r.ExpectedSHA) {
			return nil, nil, errors.New("invalid rollback request")
		}
		return nil, &r, nil
	}
	var r Release
	if err := StrictJSON(raw, &r); err != nil {
		return nil, nil, err
	}
	if err := r.Validate(); err != nil {
		return nil, nil, err
	}
	return &r, nil, nil
}
