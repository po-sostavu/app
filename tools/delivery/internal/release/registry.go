package release

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Manifest struct {
	Config    struct{ Digest string } `json:"config"`
	Manifests []struct {
		Digest   string
		Platform struct {
			OS           string
			Architecture string
		}
	} `json:"manifests"`
	Digest string `json:"-"`
}

type RegistryClient struct{ Authenticated bool }

func (c RegistryClient) Manifest(name, reference string) (*Manifest, error) {
	if !ValidName(name) || (!ValidSHA(reference) && !ValidDigest(reference)) {
		return nil, errors.New("invalid registry reference")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	query := url.Values{"service": {"ghcr.io"}, "scope": {"repository:po-sostavu/app-" + name + ":pull"}}
	request, err := http.NewRequest(http.MethodGet, "https://ghcr.io/token?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.Authenticated {
		request.SetBasicAuth(os.Getenv("GITHUB_ACTOR"), os.Getenv("GHCR_TOKEN"))
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	var token struct{ Token string }
	err = json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&token)
	_ = response.Body.Close()
	if response.StatusCode != 200 || err != nil || token.Token == "" {
		return nil, errors.New("registry authentication failed")
	}
	request, err = http.NewRequest(http.MethodGet, "https://ghcr.io/v2/po-sostavu/app-"+name+"/manifests/"+reference, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	request.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")
	response, err = client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == 404 {
		return nil, nil
	}
	if response.StatusCode != 200 {
		return nil, errors.New("registry manifest request failed")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1024*1024 {
		return nil, errors.New("registry manifest too large")
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	if response.Header.Get("Docker-Content-Digest") != digest {
		return nil, errors.New("registry digest mismatch")
	}
	var result Manifest
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result.Digest = digest
	return &result, nil
}

func (c RegistryClient) ConfigID(name string, m *Manifest) (string, error) {
	if m.Config.Digest != "" {
		return m.Config.Digest, nil
	}
	var digest string
	for _, child := range m.Manifests {
		if child.Platform.OS == "linux" && child.Platform.Architecture == "amd64" {
			if digest != "" {
				return "", errors.New("multiple amd64 images")
			}
			digest = child.Digest
		}
	}
	if digest == "" {
		return "", errors.New("missing amd64 image")
	}
	child, err := c.Manifest(name, digest)
	if err != nil {
		return "", err
	}
	if child == nil || !ValidDigest(child.Config.Digest) {
		return "", errors.New("missing platform image")
	}
	return child.Config.Digest, nil
}

type RegistryReader interface {
	Manifest(string, string) (*Manifest, error)
	ConfigID(string, *Manifest) (string, error)
}

func Publish(loaded map[string]string, sha string, runID int64, output string, registry RegistryReader, docker func(...string) ([]byte, error)) error {
	existing := map[string]*Manifest{}
	for _, name := range Names {
		m, err := registry.Manifest(name, sha)
		if err != nil {
			return err
		}
		if m != nil {
			id, err := registry.ConfigID(name, m)
			if err != nil {
				return err
			}
			if id != loaded[name] {
				return errors.New("commit tag already has another image")
			}
		}
		existing[name] = m
	}
	result := Release{Schema: 1, SHA: sha, RunID: runID, Images: map[string]string{}, RollbackCompatible: true}
	for _, name := range Names {
		if existing[name] == nil {
			tag := Registry + name + ":" + sha
			if _, err := docker("tag", loaded[name], tag); err != nil {
				return err
			}
			if _, err := docker("push", tag); err != nil {
				return err
			}
		}
		m, err := registry.Manifest(name, sha)
		if err != nil {
			return err
		}
		if m == nil {
			return errors.New("published manifest missing")
		}
		id, err := registry.ConfigID(name, m)
		if err != nil {
			return err
		}
		if id != loaded[name] {
			return errors.New("published image changed")
		}
		result.Images[name] = m.Digest
	}
	if err := result.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(raw, '\n'), 0644)
}
