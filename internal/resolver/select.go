package resolver

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/dirhash"
	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
)

// fixpoint 反复选择版本并展开传递依赖，直到选择集稳定。
//
// 每一轮都以“当前选中版本集合”为准重新收集约束并重新展开依赖：
// 某模块被升级/挤出后，旧版本带来的传递约束会随之消失，
// 从而保证选择集收敛到唯一结论，且与迭代顺序无关（天然确定）。
func (r *Resolver) fixpoint() {
	for round := 0; round < 1000; round++ {
		// 每轮：重建根约束 -> 展开全部“当前已选中”版本的传递依赖 ->
		// 由全部约束重新选择。新选中的模块在下一轮展开，直到选择集不再变化。
		r.reqs = map[string][]reqConstraint{}
		r.edges = map[string][]string{}
		for _, req := range r.root.Requires {
			r.addRequire(req.Path, req.Version, req.Kind, "根清单")
		}
		for _, p := range r.sortedSelectionPaths() {
			r.expandInto(r.sel[p])
		}

		next := map[string]*Selection{}
		// 选择按路径排序迭代，保证顺序确定；一轮中同序处理。
		for _, p := range r.sortedRequirementPaths() {
			if sel, ok := r.selectOne(p); ok {
				next[p] = sel
			}
		}
		if sameSelections(r.sel, next) {
			return
		}
		r.sel = next
	}
	r.addIssue(Issue{Type: IssueError, Kind: "malformed", Module: "",
		Reason: "依赖解析在 1000 轮内未收敛（可能存在不稳定的版本/分支声明）"})
}

// selectOne 汇总某模块的全部约束，按确定规则选择唯一版本。
func (r *Resolver) selectOne(path string) (*Selection, bool) {
	constraints := r.reqs[path]
	if len(constraints) == 0 {
		return nil, false
	}

	if t, from, ok := r.effectiveReplace(path, constraints); ok {
		return r.selectReplaced(path, constraints, t, from)
	}

	var exactPins []string
	var ranges []semver.Constraint
	var rangeRaws []string
	contribs := make([]Contribution, 0, len(constraints))
	for _, c := range constraints {
		contribs = append(contribs, Contribution{Source: c.source, Raw: c.raw, Kind: c.kind})
		if c.exact != "" {
			exactPins = append(exactPins, c.exact)
			continue
		}
		cs, err := semver.ParseConstraint(c.raw)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "malformed", Module: path,
				Reason: fmt.Sprintf("来源 %s 的区间 %q 非法：%v", c.source, c.raw, err)})
			continue
		}
		ranges = append(ranges, cs)
		rangeRaws = append(rangeRaws, c.raw)
	}

	mod, err := r.reg.Get(path)
	if err != nil {
		r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
		return nil, false
	}

	uniqueExact := uniqueSorted(exactPins)
	if len(uniqueExact) > 1 {
		r.addIssue(Issue{Type: IssueConflict, Kind: "exact", Module: path,
			Reason: fmt.Sprintf("不同来源要求互不一致的精确版本：%s（来源：%s）",
				strings.Join(uniqueExact, " 与 "), joinSources(constraints))})
		return nil, false
	}

	var filtered []semver.Version
	for _, v := range mod.SortedVersions() {
		if r.excludes[path][v.String()] {
			continue
		}
		ok := true
		for _, cs := range ranges {
			if !cs.Match(v) {
				ok = false
				break
			}
		}
		if ok {
			filtered = append(filtered, v)
		}
	}

	if len(uniqueExact) == 1 {
		want := uniqueExact[0]
		if r.excludes[path][want] {
			r.addIssue(Issue{Type: IssueConflict, Kind: "excluded", Module: path,
				Reason: fmt.Sprintf("唯一可选的 %s 已被 exclude 排除", want)})
			return nil, false
		}
		matched := false
		for _, v := range filtered {
			if v.String() == want {
				matched = true
			}
		}
		if !matched {
			r.addIssue(Issue{Type: IssueConflict, Kind: "range", Module: path,
				Reason: fmt.Sprintf("精确版本 %s 不满足区间约束 %s（来源：%s）",
					want, strings.Join(rangeRaws, " 且 "), joinSources(constraints))})
			return nil, false
		}
		rel, err := r.reg.Release(path, want)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
			return nil, false
		}
		return &Selection{Path: path, Version: want, Digest: rel.Digest, Contributors: dedupContribs(contribs)}, true
	}

	if len(filtered) == 0 {
		r.addIssue(Issue{Type: IssueConflict, Kind: "range", Module: path,
			Reason: fmt.Sprintf("约束 %s 在全部已知版本中没有交集候选", strings.Join(rangeRaws, " 且 "))})
		return nil, false
	}
	chosen := filtered[len(filtered)-1]
	rel, err := r.reg.Release(path, chosen.String())
	if err != nil {
		r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
		return nil, false
	}
	return &Selection{Path: path, Version: chosen.String(), Digest: rel.Digest, Contributors: dedupContribs(contribs)}, true
}

// effectiveReplace 决定某模块在当前约束下应使用的 replace 规则。
// 存在精确钉版时优先匹配“指定版本”规则，否则使用“全版本”规则。
func (r *Resolver) effectiveReplace(path string, constraints []reqConstraint) (replTarget, string, bool) {
	pinned := map[string]bool{}
	for _, c := range constraints {
		if c.exact != "" {
			pinned[c.exact] = true
		}
	}
	var vers []string
	for v := range pinned {
		vers = append(vers, v)
	}
	sort.Strings(vers)
	for _, v := range vers {
		if t, ok := r.repl[replKey{path: path, version: v}]; ok {
			t.oldVersion = v
			return t, fmt.Sprintf("%s %s", path, v), true
		}
	}
	// 仅当不存在任何约束钉版时，全版本替换才生效；
	// 钉版存在时应由上面的“指定版本”规则精确匹配，避免越权重定向。
	for _, c := range constraints {
		if c.exact != "" {
			// 钉版在注册中心不存在，但存在全版本 replace（常见于本地开发替换）：
			// 允许回落到该 replace；钉版真实存在时则绝不越权重定向。
			if _, err := r.reg.Release(path, c.exact); err != nil {
				if t, ok := r.repl[replKey{path: path}]; ok {
					t.oldVersion = ""
					return t, path + "（全版本）", true
				}
			}
			return replTarget{}, "", false
		}
	}
	t, ok := r.repl[replKey{path: path}]
	if ok {
		t.oldVersion = ""
		return t, path + "（全版本）", true
	}
	return replTarget{}, "", false
}

// selectReplaced 处理 replace：目标版本固定，但仍需与所有约束相容；
// 本地目录替换则计算目录摘要，且没有传递依赖。
func (r *Resolver) selectReplaced(path string, constraints []reqConstraint, t replTarget, from string) (*Selection, bool) {
	var contribs []Contribution
	for _, c := range constraints {
		contribs = append(contribs, Contribution{Source: c.source, Raw: c.raw, Kind: c.kind})
	}
	if t.localPath != "" {
		abs := t.localPath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(r.rootDir, abs)
		}
		digest, err := dirhash.HashDir(abs)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
			return nil, false
		}
		return &Selection{
			Path: path, Version: "v0.0.0-local", Digest: digest,
			LocalPath: filepath.ToSlash(t.localPath), Local: true,
			ReplacedFrom: from, Contributors: dedupContribs(contribs),
		}, true
	}

	want, err := semver.Parse(t.newVersion)
	if err != nil {
		r.addIssue(Issue{Type: IssueError, Kind: "malformed", Module: path, Reason: err.Error()})
		return nil, false
	}
	for _, c := range constraints {
		if c.exact != "" && c.exact != t.newVersion && c.exact != t.oldVersion {
			r.addIssue(Issue{Type: IssueConflict, Kind: "replace", Module: path,
				Reason: fmt.Sprintf("replace 将模块固定为 %s，但来源 %s 要求 %s", t.newVersion, c.source, c.exact)})
			return nil, false
		}
		if c.exact == "" {
			cs, err := semver.ParseConstraint(c.raw)
			if err == nil && !cs.Match(want) {
				r.addIssue(Issue{Type: IssueConflict, Kind: "replace", Module: path,
					Reason: fmt.Sprintf("replace 固定版本 %s 不满足来源 %s 的区间 %s", t.newVersion, c.source, c.raw)})
				return nil, false
			}
		}
	}
	if r.excludes[path] != nil && r.excludes[path][t.newVersion] {
		r.addIssue(Issue{Type: IssueConflict, Kind: "excluded", Module: t.newPath,
			Reason: fmt.Sprintf("replace 目标 %s %s 已被 exclude 排除", t.newPath, t.newVersion)})
		return nil, false
	}
	rel, err := r.reg.Release(t.newPath, t.newVersion)
	if err != nil {
		r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: t.newPath, Reason: err.Error()})
		return nil, false
	}
	return &Selection{
		Path: path, Version: t.newVersion, Digest: rel.Digest,
		ReplacedFrom: from, Contributors: dedupContribs(contribs),
		realPath: t.newPath,
	}, true
}
