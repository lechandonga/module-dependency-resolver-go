// Package lock generates lock files from resolution results and verifies
// them against the current manifest and registry, reporting categorized
// drift errors instead of silently repairing or accepting them.
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/lechandonga/module-dependency-resolver-go/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/registry"
	"github.com/lechandonga/module-dependency-resolver-go/resolver"
)

// DriftKind categorizes lock verification failures.
type DriftKind string

const (
	DriftManifestChanged DriftKind = "manifest_changed"    // manifest content differs from what was locked
	DriftMissingEntry    DriftKind = "missing_entry"       // resolution selects a module absent from the lock
	DriftStaleEntry      DriftKind = "stale_entry"         // lock contains a module no longer referenced
	DriftVersionMismatch DriftKind = "version_mismatch"    // locked version differs from fresh resolution
	DriftDigestMismatch  DriftKind = "digest_mismatch"     // locked digest differs from registry content
	DriftConflict        DriftKind = "resolution_conflict" // fresh resolution has conflicts
)

// Drift is a single verification failure.
type Drift struct {
	Kind   DriftKind `json:"kind"`
	Path   string    `json:"path,omitempty"`
	Detail string    `json:"detail"`
}

func (d *Drift) Error() string {
	if d.Path != "" {
		return fmt.Sprintf("%s: %s: %s", d.Kind, d.Path, d.Detail)
	}
	return fmt.Sprintf("%s: %s", d.Kind, d.Detail)
}

// Drifts is a collection of verification failures.
type Drifts []*Drift

func (ds Drifts) Error() string {
	s := fmt.Sprintf("%d lock drift error(s):", len(ds))
	for _, d := range ds {
		s += "\n  - " + d.Error()
	}
	return s
}

// Entry is one locked module.
type Entry struct {
	Path       string `json:"path"`
	Version    string `json:"version"`
	Digest     string `json:"digest"`
	ReplacedBy string `json:"replaced_by,omitempty"`
}

// File is the on-disk lock format.
type File struct {
	ManifestHash string  `json:"manifest_hash"` // sha256 of the canonical manifest bytes
	Entries      []Entry `json:"entries"`
}

// ManifestHash computes the canonical hash of manifest JSON bytes.
func ManifestHash(manifestData []byte) string {
	sum := sha256.Sum256(manifestData)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Generate resolves the manifest and builds a lock file. It fails if the
// resolution has conflicts.
func Generate(manifestData []byte, m *manifest.Manifest, reg *registry.Registry) (*File, error) {
	res := resolver.Resolve(m, reg)
	if !res.Ok() {
		var ds Drifts
		for _, c := range res.Conflicts {
			ds = append(ds, &Drift{Kind: DriftConflict, Path: c.Path, Detail: c.Error()})
		}
		return nil, ds
	}
	f := &File{ManifestHash: ManifestHash(manifestData)}
	for _, e := range res.Entries() {
		f.Entries = append(f.Entries, Entry{Path: e.Path, Version: e.Version, Digest: e.Digest, ReplacedBy: e.ReplacedBy})
	}
	return f, nil
}

// Marshal serializes the lock deterministically (entries sorted by path).
func (f *File) Marshal() ([]byte, error) {
	entries := append([]Entry{}, f.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	out := &File{ManifestHash: f.ManifestHash, Entries: entries}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteFile writes the lock to disk.
func (f *File) WriteFile(name string) error {
	data, err := f.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// ReadFile loads a lock from disk.
func ReadFile(name string) (*File, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("lock file: %w", err)
	}
	return &f, nil
}

// Verify checks the lock against the current manifest bytes and registry.
// Every mismatch is reported with its own DriftKind; nothing is repaired.
func Verify(f *File, manifestData []byte, m *manifest.Manifest, reg *registry.Registry) error {
	var drifts Drifts

	if got := ManifestHash(manifestData); got != f.ManifestHash {
		drifts = append(drifts, &Drift{
			Kind:   DriftManifestChanged,
			Detail: fmt.Sprintf("manifest hash is %s, lock records %s", got, f.ManifestHash),
		})
	}

	res := resolver.Resolve(m, reg)
	for _, c := range res.Conflicts {
		drifts = append(drifts, &Drift{Kind: DriftConflict, Path: c.Path, Detail: c.Error()})
	}

	locked := map[string]Entry{}
	for _, e := range f.Entries {
		locked[e.Path] = e
	}
	fresh := map[string]resolver.Selected{}
	for _, e := range res.Entries() {
		fresh[e.Path] = e
	}

	for path, sel := range fresh {
		e, ok := locked[path]
		if !ok {
			drifts = append(drifts, &Drift{Kind: DriftMissingEntry, Path: path, Detail: "selected by resolution but absent from lock"})
			continue
		}
		if e.Version != sel.Version {
			drifts = append(drifts, &Drift{
				Kind: DriftVersionMismatch, Path: path,
				Detail: fmt.Sprintf("lock has %s, fresh resolution selects %s", e.Version, sel.Version),
			})
		}
		if e.Digest != sel.Digest {
			drifts = append(drifts, &Drift{
				Kind: DriftDigestMismatch, Path: path,
				Detail: fmt.Sprintf("lock digest %s does not match registry content digest %s", e.Digest, sel.Digest),
			})
		}
	}
	for path := range locked {
		if _, ok := fresh[path]; !ok {
			drifts = append(drifts, &Drift{Kind: DriftStaleEntry, Path: path, Detail: "locked module is no longer referenced by the manifest"})
		}
	}

	sort.Slice(drifts, func(i, j int) bool {
		if drifts[i].Kind != drifts[j].Kind {
			return drifts[i].Kind < drifts[j].Kind
		}
		return drifts[i].Path < drifts[j].Path
	})
	if len(drifts) > 0 {
		return drifts
	}
	return nil
}
