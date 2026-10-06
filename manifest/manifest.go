// Package manifest parses local dependency manifest files into a normalized
// form. Every malformed entry is reported with a categorized error that
// pinpoints the offending declaration.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
)

// ErrorKind categorizes manifest parse failures.
type ErrorKind string

const (
	ErrSyntax        ErrorKind = "syntax_error"        // file is not valid JSON / wrong shape
	ErrInvalidVer    ErrorKind = "invalid_version"     // bad version string
	ErrInvalidRange  ErrorKind = "invalid_range"       // malformed or empty version range
	ErrMissingField  ErrorKind = "missing_field"       // required field absent
	ErrUnknownField  ErrorKind = "unknown_field"       // directive field not recognized
	ErrDuplicateDecl ErrorKind = "duplicate_directive" // same directive declared twice
)

// Error describes a single malformed declaration in a manifest.
type Error struct {
	Kind      ErrorKind `json:"kind"`
	Directive string    `json:"directive"` // require / exclude / replace / module
	Index     int       `json:"index"`     // entry index within the directive list
	Path      string    `json:"path"`      // module path involved, if known
	Detail    string    `json:"detail"`
}

func (e *Error) Error() string {
	loc := e.Directive
	if e.Index >= 0 {
		loc = fmt.Sprintf("%s[%d]", e.Directive, e.Index)
	}
	if e.Path != "" {
		loc += "(" + e.Path + ")"
	}
	return fmt.Sprintf("%s: %s: %s", loc, e.Kind, e.Detail)
}

// Errors is a collection of manifest errors.
type Errors []*Error

func (es Errors) Error() string {
	s := fmt.Sprintf("%d manifest error(s):", len(es))
	for _, e := range es {
		s += "\n  - " + e.Error()
	}
	return s
}

// Requirement is a normalized require entry. Exactly one of Constraint
// (exact version or range) or Branch is set.
type Requirement struct {
	Path       string
	Constraint *semver.Constraint // exact version or range
	Branch     string             // branch reference, e.g. "main"
}

func (r Requirement) Spec() string {
	if r.Branch != "" {
		return "branch:" + r.Branch
	}
	return r.Constraint.String()
}

// Replace pins a module path to another path (optionally at a version).
type Replace struct {
	Path    string
	With    string
	Version *semver.Version // nil means "any version of With"
}

// Manifest is the normalized root module declaration.
type Manifest struct {
	Module   string
	Requires []Requirement
	Excludes map[string][]semver.Version // path -> excluded exact versions
	Replaces []Replace
}

// raw JSON shapes. "version" accepts exact versions, pseudo-versions and
// ranges; "ref" accepts "branch:<name>".
type rawManifest struct {
	Module   string          `json:"module"`
	Requires []rawRequire    `json:"requires"`
	Excludes []rawExclude    `json:"excludes"`
	Replaces []rawReplace    `json:"replaces"`
	Extra    json.RawMessage `json:"-"`
}

type rawRequire struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Ref     string `json:"ref"`
}

type rawExclude struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type rawReplace struct {
	Path    string `json:"path"`
	With    string `json:"with"`
	Version string `json:"version"`
}

// ParseFile reads and parses a manifest file.
func ParseFile(name string) (*Manifest, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses manifest JSON, collecting all entry-level errors instead of
// silently skipping bad declarations.
func Parse(data []byte) (*Manifest, error) {
	var raw rawManifest
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, &Error{Kind: ErrSyntax, Directive: "manifest", Index: -1, Detail: err.Error()}
	}
	var errs Errors
	m := &Manifest{Excludes: map[string][]semver.Version{}}

	if raw.Module == "" {
		errs = append(errs, &Error{Kind: ErrMissingField, Directive: "module", Index: -1, Detail: "module path is required"})
	} else {
		m.Module = raw.Module
	}

	seenReq := map[string]bool{}
	for i, r := range raw.Requires {
		if r.Path == "" {
			errs = append(errs, &Error{Kind: ErrMissingField, Directive: "require", Index: i, Detail: "path is required"})
			continue
		}
		if seenReq[r.Path] {
			errs = append(errs, &Error{Kind: ErrDuplicateDecl, Directive: "require", Index: i, Path: r.Path, Detail: "module required more than once; merge the constraints"})
			continue
		}
		seenReq[r.Path] = true
		req, err := parseRequire(r)
		if err != nil {
			err.Index = i
			err.Path = r.Path
			errs = append(errs, err)
			continue
		}
		m.Requires = append(m.Requires, *req)
	}

	for i, e := range raw.Excludes {
		if e.Path == "" {
			errs = append(errs, &Error{Kind: ErrMissingField, Directive: "exclude", Index: i, Detail: "path is required"})
			continue
		}
		if e.Version == "" {
			errs = append(errs, &Error{Kind: ErrMissingField, Directive: "exclude", Index: i, Path: e.Path, Detail: "version is required"})
			continue
		}
		v, err := semver.Parse(e.Version)
		if err != nil {
			errs = append(errs, &Error{Kind: ErrInvalidVer, Directive: "exclude", Index: i, Path: e.Path, Detail: err.Error()})
			continue
		}
		m.Excludes[e.Path] = append(m.Excludes[e.Path], v)
	}

	seenRep := map[string]bool{}
	for i, r := range raw.Replaces {
		if r.Path == "" || r.With == "" {
			errs = append(errs, &Error{Kind: ErrMissingField, Directive: "replace", Index: i, Path: r.Path, Detail: "both path and with are required"})
			continue
		}
		if seenRep[r.Path] {
			errs = append(errs, &Error{Kind: ErrDuplicateDecl, Directive: "replace", Index: i, Path: r.Path, Detail: "module replaced more than once"})
			continue
		}
		seenRep[r.Path] = true
		rep := Replace{Path: r.Path, With: r.With}
		if r.Version != "" {
			v, err := semver.Parse(r.Version)
			if err != nil {
				errs = append(errs, &Error{Kind: ErrInvalidVer, Directive: "replace", Index: i, Path: r.Path, Detail: err.Error()})
				continue
			}
			rep.Version = &v
		}
		m.Replaces = append(m.Replaces, rep)
	}

	if len(errs) > 0 {
		return nil, errs
	}
	sort.Slice(m.Requires, func(i, j int) bool { return m.Requires[i].Path < m.Requires[j].Path })
	return m, nil
}

func parseRequire(r rawRequire) (*Requirement, *Error) {
	if r.Version == "" && r.Ref == "" {
		return nil, &Error{Kind: ErrMissingField, Directive: "require", Index: -1, Detail: "either version or ref is required"}
	}
	if r.Version != "" && r.Ref != "" {
		return nil, &Error{Kind: ErrUnknownField, Directive: "require", Index: -1, Detail: "version and ref are mutually exclusive"}
	}
	if r.Ref != "" {
		const prefix = "branch:"
		if len(r.Ref) <= len(prefix) || r.Ref[:len(prefix)] != prefix {
			return nil, &Error{Kind: ErrUnknownField, Directive: "require", Index: -1, Detail: fmt.Sprintf("unsupported ref %q: only branch:<name> is supported", r.Ref)}
		}
		return &Requirement{Path: r.Path, Branch: r.Ref[len(prefix):]}, nil
	}
	c, err := semver.ParseConstraint(r.Version)
	if err != nil {
		kind := ErrInvalidVer
		if len(r.Version) > 0 && (r.Version[0] == '[' || r.Version[0] == '(' || containsRangeOp(r.Version)) {
			kind = ErrInvalidRange
		}
		return nil, &Error{Kind: kind, Directive: "require", Index: -1, Detail: err.Error()}
	}
	return &Requirement{Path: r.Path, Constraint: &c}, nil
}

func containsRangeOp(s string) bool {
	for _, ch := range s {
		if ch == '>' || ch == '<' || ch == ',' || ch == ' ' {
			return true
		}
	}
	return false
}
