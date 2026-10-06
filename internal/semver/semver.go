// Package semver implements parsing and comparison of semantic versions,
// pseudo-versions, and version range constraints used by the resolver.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Pseudo-versions
// (vX.Y.Z-yyyymmddhhmmss-abcdef123456) are supported and ordered by their
// base version and commit timestamp.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // pseudo-version timestamp+commit suffix
	Raw                 string
}

var (
	versionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
	pseudoRe  = regexp.MustCompile(`^(\d{14})-[0-9a-fA-F]{12}$`)
)

// Parse parses a version string like "v1.2.3" or a pseudo-version like
// "v0.0.0-20240102030405-abcdef123456".
func Parse(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q: want vMAJOR.MINOR.PATCH or a pseudo-version", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	pre := m[4]
	if pre != "" && !pseudoRe.MatchString(pre) {
		return Version{}, fmt.Errorf("invalid version %q: suffix is not a pseudo-version (14-digit timestamp plus 12-hex commit)", s)
	}
	return Version{Major: major, Minor: minor, Patch: patch, Pre: pre, Raw: s}, nil
}

// IsPseudo reports whether v is a pseudo-version.
func (v Version) IsPseudo() bool { return v.Pre != "" }

func (v Version) String() string { return v.Raw }

// Compare returns -1, 0 or 1. Pseudo-versions sort before the plain release
// with the same base, ordered by timestamp.
func Compare(a, b Version) int {
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}
	switch {
	case a.Pre == "" && b.Pre == "":
		return 0
	case a.Pre == "":
		return 1 // release > pseudo of same base
	case b.Pre == "":
		return -1
	default:
		return strings.Compare(a.Pre, b.Pre)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Constraint is a set of version comparators. A version matches if it
// satisfies every comparator.
type Constraint struct {
	comparators []comparator
	raw         string
}

type comparator struct {
	op  string // ">=", ">", "<=", "<", "=="
	ver Version
}

// ParseConstraint parses a version specification. Supported forms:
//
//	"v1.2.3"                    exact version (or pseudo-version)
//	">=1.0.0 <2.0.0"            comparator list (comma or space separated)
//	"[1.0.0,2.0.0)"             interval notation
func ParseConstraint(s string) (Constraint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Constraint{}, fmt.Errorf("empty version constraint")
	}
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "(") {
		return parseInterval(s)
	}
	var comps []comparator
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if tok == "" {
			continue
		}
		op := "=="
		rest := tok
		for _, p := range []string{">=", "<=", "==", ">", "<"} {
			if strings.HasPrefix(tok, p) {
				op = p
				rest = strings.TrimPrefix(tok, p)
				break
			}
		}
		ver, err := Parse(withV(rest))
		if err != nil {
			return Constraint{}, fmt.Errorf("invalid constraint %q: %v", s, err)
		}
		comps = append(comps, comparator{op: op, ver: ver})
	}
	if len(comps) == 0 {
		return Constraint{}, fmt.Errorf("empty version constraint")
	}
	c := Constraint{comparators: comps, raw: s}
	if !c.sane() {
		return Constraint{}, fmt.Errorf("invalid constraint %q: range is empty (lower bound >= upper bound)", s)
	}
	return c, nil
}

func parseInterval(s string) (Constraint, error) {
	if len(s) < 2 {
		return Constraint{}, fmt.Errorf("invalid interval %q", s)
	}
	open, close := s[0], s[len(s)-1]
	if (open != '[' && open != '(') || (close != ']' && close != ')') {
		return Constraint{}, fmt.Errorf("invalid interval %q: must look like [a,b) or (a,b]", s)
	}
	parts := strings.Split(s[1:len(s)-1], ",")
	if len(parts) != 2 {
		return Constraint{}, fmt.Errorf("invalid interval %q: want exactly two bounds", s)
	}
	lo, err := Parse(withV(parts[0]))
	if err != nil {
		return Constraint{}, fmt.Errorf("invalid interval %q lower bound: %v", s, err)
	}
	hi, err := Parse(withV(parts[1]))
	if err != nil {
		return Constraint{}, fmt.Errorf("invalid interval %q upper bound: %v", s, err)
	}
	loOp, hiOp := ">", "<"
	if open == '[' {
		loOp = ">="
	}
	if close == ']' {
		hiOp = "<="
	}
	c := Constraint{comparators: []comparator{{loOp, lo}, {hiOp, hi}}, raw: s}
	if !c.sane() {
		return Constraint{}, fmt.Errorf("invalid interval %q: range is empty (lower bound >= upper bound)", s)
	}
	return c, nil
}

// sane reports whether the constraint is not obviously empty, by comparing
// the strongest lower and upper bounds.
func (c Constraint) sane() bool {
	var lo, hi *comparator
	for i := range c.comparators {
		cmp := &c.comparators[i]
		switch cmp.op {
		case ">", ">=":
			if lo == nil || Compare(cmp.ver, lo.ver) > 0 || (Compare(cmp.ver, lo.ver) == 0 && cmp.op == ">") {
				lo = cmp
			}
		case "<", "<=":
			if hi == nil || Compare(cmp.ver, hi.ver) < 0 || (Compare(cmp.ver, hi.ver) == 0 && cmp.op == "<") {
				hi = cmp
			}
		}
	}
	if lo != nil && hi != nil {
		d := Compare(lo.ver, hi.ver)
		if d > 0 {
			return false
		}
		if d == 0 && (lo.op == ">" || hi.op == "<") {
			return false
		}
	}
	return true
}

// Matches reports whether v satisfies every comparator.
func (c Constraint) Matches(v Version) bool {
	for _, cmp := range c.comparators {
		d := Compare(v, cmp.ver)
		switch cmp.op {
		case ">=":
			if d < 0 {
				return false
			}
		case ">":
			if d <= 0 {
				return false
			}
		case "<=":
			if d > 0 {
				return false
			}
		case "<":
			if d >= 0 {
				return false
			}
		case "==":
			if d != 0 {
				return false
			}
		}
	}
	return true
}

func (c Constraint) String() string { return c.raw }

// withV tolerates bounds written without the leading "v" inside intervals.
func withV(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && s[0] != 'v' {
		return "v" + s
	}
	return s
}
