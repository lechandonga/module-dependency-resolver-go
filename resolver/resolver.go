// Package resolver expands transitive dependencies from a manifest against a
// local registry, selects exactly one version per module deterministically,
// and reports conflicts instead of guessing.
package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
	"github.com/lechandonga/module-dependency-resolver-go/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/registry"
)

// ConflictKind categorizes resolution conflicts.
type ConflictKind string

const (
	ConflictNoIntersection ConflictKind = "no_version_intersection" // constraints admit no common version
	ConflictCycle          ConflictKind = "dependency_cycle"        // selected graph contains a cycle
	ConflictReplaceChain   ConflictKind = "replace_chain_conflict"  // replace directives loop or target a replaced module inconsistently
	ConflictDuplicate      ConflictKind = "duplicate_introduction"  // same module introduced by different sources with incompatible specs
	ConflictMissing        ConflictKind = "missing_module"          // module/branch/version absent from registry
)

// Conflict is a single resolution failure, with the sources that caused it.
type Conflict struct {
	Kind    ConflictKind `json:"kind"`
	Path    string       `json:"path"`
	Sources []string     `json:"sources,omitempty"`
	Detail  string       `json:"detail"`
}

func (c Conflict) Error() string {
	src := ""
	if len(c.Sources) > 0 {
		src = " (from " + strings.Join(c.Sources, ", ") + ")"
	}
	return fmt.Sprintf("%s: %s%s: %s", c.Kind, c.Path, src, c.Detail)
}

// Selected is the final decision for one module.
type Selected struct {
	Path       string `json:"path"`
	Version    string `json:"version"`
	Digest     string `json:"digest"`
	ReplacedBy string `json:"replaced_by,omitempty"` // original path, if this entry is a replace target
}

// Result is the outcome of a resolution run.
type Result struct {
	Selected  map[string]Selected
	Edges     map[string][]string // importer -> imported (selected paths)
	Conflicts []Conflict
}

// Entries returns selected modules sorted by path for deterministic output.
func (r *Result) Entries() []Selected {
	out := make([]Selected, 0, len(r.Selected))
	for _, s := range r.Selected {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Ok reports whether resolution completed without conflicts.
func (r *Result) Ok() bool { return len(r.Conflicts) == 0 }

type sourcedConstraint struct {
	constraint semver.Constraint
	source     string
}

// Resolve runs the deterministic resolution. Selection rule: for each
// module, intersect every constraint contributed by its importers, drop
// excluded versions, then pick the highest remaining published version.
// Replaces redirect a path before selection; replace chains that loop are
// conflicts. The same inputs always produce the same Result.
func Resolve(m *manifest.Manifest, reg *registry.Registry) *Result {
	res := &Result{Selected: map[string]Selected{}, Edges: map[string][]string{}}

	// Resolve replace chains once, up front.
	repl, chainConflicts := resolveReplaceChains(m)
	res.Conflicts = append(res.Conflicts, chainConflicts...)

	constraints := map[string][]sourcedConstraint{}
	excludes := m.Excludes
	dirty := map[string]bool{}
	expanded := map[string]bool{} // selected path@version already expanded

	addConstraint := func(path, spec, source string) {
		c, err := parseSpec(path, spec, reg)
		if err != nil {
			res.Conflicts = append(res.Conflicts, Conflict{Kind: ConflictMissing, Path: path, Sources: []string{source}, Detail: err.Error()})
			return
		}
		constraints[path] = append(constraints[path], sourcedConstraint{constraint: c, source: source})
		dirty[path] = true
	}

	for _, req := range m.Requires {
		if req.Branch != "" {
			v, err := reg.Branch(effectivePath(req.Path, repl), req.Branch)
			if err != nil {
				res.Conflicts = append(res.Conflicts, Conflict{Kind: ConflictMissing, Path: req.Path, Sources: []string{m.Module}, Detail: err.Error()})
				continue
			}
			c, _ := semver.ParseConstraint(v.Raw)
			constraints[req.Path] = append(constraints[req.Path], sourcedConstraint{constraint: c, source: m.Module + " (branch:" + req.Branch + ")"})
			dirty[req.Path] = true
			continue
		}
		constraints[req.Path] = append(constraints[req.Path], sourcedConstraint{constraint: *req.Constraint, source: m.Module})
		dirty[req.Path] = true
	}

	// Worklist loop: re-select any module whose constraint set changed.
	for {
		var path string
		for p := range dirty {
			if path == "" || p < path {
				path = p
			}
		}
		if path == "" {
			break
		}
		delete(dirty, path)

		eff := effectivePath(path, repl)
		rep, hasRep := repl[path]
		sel, conflict := selectVersion(path, eff, constraints[path], excludes[path], rep, hasRep, reg)
		if conflict != nil {
			res.Conflicts = append(res.Conflicts, *conflict)
			delete(res.Selected, path)
			continue
		}
		if prev, ok := res.Selected[path]; ok && prev.Version == sel.Version {
			// unchanged; still may need expansion below
		}
		res.Selected[path] = *sel

		key := path + "@" + sel.Version
		if expanded[key] {
			continue
		}
		expanded[key] = true
		reqs, err := reg.Requires(eff, mustParse(sel.Version))
		if err != nil {
			res.Conflicts = append(res.Conflicts, Conflict{Kind: ConflictMissing, Path: eff, Detail: err.Error()})
			continue
		}
		src := path + "@" + sel.Version
		for depPath, spec := range reqs {
			res.Edges[path] = appendUnique(res.Edges[path], depPath)
			addConstraint(depPath, spec, src)
		}
		sort.Strings(res.Edges[path])
	}

	res.Conflicts = append(res.Conflicts, detectCycles(res.Edges)...)
	sort.Slice(res.Conflicts, func(i, j int) bool {
		if res.Conflicts[i].Kind != res.Conflicts[j].Kind {
			return res.Conflicts[i].Kind < res.Conflicts[j].Kind
		}
		return res.Conflicts[i].Path < res.Conflicts[j].Path
	})
	return res
}

// parseSpec parses a registry/manifest version spec: "branch:<name>",
// exact/pseudo version, or range.
func parseSpec(path, spec string, reg *registry.Registry) (semver.Constraint, error) {
	if strings.HasPrefix(spec, "branch:") {
		v, err := reg.Branch(path, strings.TrimPrefix(spec, "branch:"))
		if err != nil {
			return semver.Constraint{}, err
		}
		return semver.ParseConstraint(v.Raw)
	}
	return semver.ParseConstraint(spec)
}

// resolveReplaceChains follows replace directives to their final target and
// reports self-referential or looping chains as conflicts.
func resolveReplaceChains(m *manifest.Manifest) (map[string]manifest.Replace, []Conflict) {
	direct := map[string]manifest.Replace{}
	for _, r := range m.Replaces {
		direct[r.Path] = r
	}
	final := map[string]manifest.Replace{}
	var conflicts []Conflict
	for _, r := range m.Replaces {
		seen := map[string]bool{r.Path: true}
		cur := r
		for {
			next, ok := direct[cur.With]
			if !ok {
				break
			}
			if seen[cur.With] {
				conflicts = append(conflicts, Conflict{
					Kind: ConflictReplaceChain, Path: r.Path,
					Detail: fmt.Sprintf("replace chain loops: %s -> %s -> %s", r.Path, cur.With, next.With),
				})
				break
			}
			seen[cur.With] = true
			if next.Version != nil && cur.Version != nil && *next.Version != *cur.Version {
				conflicts = append(conflicts, Conflict{
					Kind: ConflictReplaceChain, Path: r.Path,
					Detail: fmt.Sprintf("replace chain pins contradictory versions %s and %s at %s", cur.Version, next.Version, cur.With),
				})
			}
			cur = manifest.Replace{Path: r.Path, With: next.With, Version: next.Version}
			if cur.Version == nil {
				cur.Version = r.Version
			}
		}
		final[r.Path] = cur
	}
	return final, conflicts
}

func effectivePath(path string, repl map[string]manifest.Replace) string {
	if r, ok := repl[path]; ok {
		return r.With
	}
	return path
}

func selectVersion(origPath, effPath string, cons []sourcedConstraint, excluded []semver.Version, rep manifest.Replace, hasRep bool, reg *registry.Registry) (*Selected, *Conflict) {
	sources := make([]string, 0, len(cons))
	for _, c := range cons {
		sources = append(sources, c.source)
	}
	sort.Strings(sources)

	versions, err := reg.Versions(effPath)
	if err != nil {
		return nil, &Conflict{Kind: ConflictMissing, Path: origPath, Sources: sources, Detail: err.Error()}
	}

	// A replace with a pinned version acts as an exact constraint.
	if hasRep && rep.Version != nil {
		c, _ := semver.ParseConstraint(rep.Version.Raw)
		cons = append(cons, sourcedConstraint{constraint: c, source: "replace directive"})
	}

	var candidates []semver.Version
	for _, v := range versions {
		ok := true
		for _, c := range cons {
			if !c.constraint.Matches(v) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		skip := false
		for _, ex := range excluded {
			if semver.Compare(v, ex) == 0 {
				skip = true
				break
			}
		}
		if !skip {
			candidates = append(candidates, v)
		}
	}

	if len(candidates) == 0 {
		kind := ConflictNoIntersection
		detail := "no published version satisfies all constraints"
		if len(distinctSources(sources)) > 1 {
			kind = ConflictDuplicate
			detail = "module introduced by multiple sources whose constraints admit no common version"
		}
		if hasRep {
			detail += fmt.Sprintf(" (path replaced by %s)", effPath)
		}
		return nil, &Conflict{Kind: kind, Path: origPath, Sources: sources, Detail: detail}
	}

	best := candidates[0]
	for _, v := range candidates[1:] {
		if semver.Compare(v, best) > 0 {
			best = v
		}
	}
	digest, err := reg.Digest(effPath, best)
	if err != nil {
		return nil, &Conflict{Kind: ConflictMissing, Path: origPath, Sources: sources, Detail: err.Error()}
	}
	sel := &Selected{Path: effPath, Version: best.Raw, Digest: digest}
	if effPath != origPath {
		sel.ReplacedBy = origPath
	}
	return sel, nil
}

func distinctSources(sources []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sources {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// detectCycles finds cycles in the selected dependency graph via DFS.
func detectCycles(edges map[string][]string) []Conflict {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var conflicts []Conflict
	seenCycle := map[string]bool{}
	var stack []string
	var visit func(n string)
	visit = func(n string) {
		color[n] = gray
		stack = append(stack, n)
		for _, next := range edges[n] {
			if color[next] == gray {
				// extract cycle path
				idx := 0
				for i, s := range stack {
					if s == next {
						idx = i
						break
					}
				}
				cycle := append(append([]string{}, stack[idx:]...), next)
				key := strings.Join(cycle, "->")
				if !seenCycle[key] {
					seenCycle[key] = true
					conflicts = append(conflicts, Conflict{
						Kind: ConflictCycle, Path: next,
						Detail: "dependency cycle: " + key,
					})
				}
			} else if color[next] == white {
				visit(next)
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	var nodes []string
	for n := range edges {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if color[n] == white {
			visit(n)
		}
	}
	return conflicts
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func mustParse(v string) semver.Version {
	ver, err := semver.Parse(v)
	if err != nil {
		panic(err)
	}
	return ver
}
