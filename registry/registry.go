// Package registry simulates a module source from a local JSON file:
// available versions per module, each version's own requirements, content
// for digesting, and branch heads for branch references.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
)

// Version is one available version of a module.
type Version struct {
	Version  string            `json:"version"`
	Content  string            `json:"content"`            // arbitrary payload; digest is derived from it
	Requires map[string]string `json:"requires,omitempty"` // path -> version spec (same syntax as manifests)
}

// Module describes all published data for one module path.
type Module struct {
	Path     string            `json:"path"`
	Versions []Version         `json:"versions"`
	Branches map[string]string `json:"branches,omitempty"` // branch name -> version it currently points to
}

// Registry is the local module store.
type Registry struct {
	modules map[string]*Module
}

// Load reads a registry file of the form {"modules": [Module, ...]}.
func Load(name string) (*Registry, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse builds a Registry from JSON bytes.
func Parse(data []byte) (*Registry, error) {
	var raw struct {
		Modules []Module `json:"modules"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	r := &Registry{modules: map[string]*Module{}}
	for i := range raw.Modules {
		mod := &raw.Modules[i]
		if mod.Path == "" {
			return nil, fmt.Errorf("registry: module at index %d has no path", i)
		}
		if _, dup := r.modules[mod.Path]; dup {
			return nil, fmt.Errorf("registry: module %q defined twice", mod.Path)
		}
		for _, v := range mod.Versions {
			if _, err := semver.Parse(v.Version); err != nil {
				return nil, fmt.Errorf("registry: %s: %v", mod.Path, err)
			}
		}
		for br, ver := range mod.Branches {
			if _, err := semver.Parse(ver); err != nil {
				return nil, fmt.Errorf("registry: %s branch %q: %v", mod.Path, br, err)
			}
		}
		r.modules[mod.Path] = mod
	}
	return r, nil
}

// Has reports whether the module path exists in the registry.
func (r *Registry) Has(path string) bool { _, ok := r.modules[path]; return ok }

// Versions returns all published versions of path, sorted ascending.
func (r *Registry) Versions(path string) ([]semver.Version, error) {
	mod, ok := r.modules[path]
	if !ok {
		return nil, fmt.Errorf("module %q not found in registry", path)
	}
	var out []semver.Version
	for _, v := range mod.Versions {
		ver, _ := semver.Parse(v.Version)
		out = append(out, ver)
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare(out[i], out[j]) < 0 })
	return out, nil
}

// Branch resolves a branch reference to the version it currently points to.
func (r *Registry) Branch(path, branch string) (semver.Version, error) {
	mod, ok := r.modules[path]
	if !ok {
		return semver.Version{}, fmt.Errorf("module %q not found in registry", path)
	}
	ver, ok := mod.Branches[branch]
	if !ok {
		return semver.Version{}, fmt.Errorf("module %q has no branch %q", path, branch)
	}
	return semver.Parse(ver)
}

// Info returns the stored version record for path@version.
func (r *Registry) Info(path string, v semver.Version) (*Version, error) {
	mod, ok := r.modules[path]
	if !ok {
		return nil, fmt.Errorf("module %q not found in registry", path)
	}
	for i := range mod.Versions {
		if mod.Versions[i].Version == v.Raw {
			return &mod.Versions[i], nil
		}
	}
	return nil, fmt.Errorf("module %q has no version %s", path, v)
}

// Digest computes the content digest of path@version.
func (r *Registry) Digest(path string, v semver.Version) (string, error) {
	info, err := r.Info(path, v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(path + "\x00" + v.Raw + "\x00" + info.Content))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Requires returns the normalized requirements of path@version.
func (r *Registry) Requires(path string, v semver.Version) (map[string]string, error) {
	info, err := r.Info(path, v)
	if err != nil {
		return nil, err
	}
	return info.Requires, nil
}
