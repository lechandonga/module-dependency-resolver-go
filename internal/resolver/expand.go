package resolver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
)

// expandInto 读取选中版本的依赖（本地替换目录读取其中的 modules.txt 清单），
// 把约束写入本轮 r.reqs，并在依赖图中记录边。
func (r *Resolver) expandInto(sel *Selection) {
	var deps []registry.RequireRef
	from := sel.Path + " " + sel.Version

	if sel.Local {
		abs := sel.LocalPath
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(r.rootDir, abs)
		}
		manifestFile := filepath.Join(abs, "modules.txt")
		data, err := os.ReadFile(manifestFile)
		if err != nil {
			if os.IsNotExist(err) {
				return // 本地目录没有 modules.txt，视为叶子模块
			}
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: sel.Path, Reason: err.Error()})
			return
		}
		sub, errs := manifest.Parse(strings.NewReader(string(data)), manifestFile)
		for _, e := range errs {
			r.addIssue(Issue{Type: IssueError, Kind: "malformed", Module: sel.Path, Reason: e.Error()})
		}
		if len(errs) > 0 {
			return
		}
		for _, q := range sub.Requires {
			deps = append(deps, registry.RequireRef{Path: q.Path, Version: q.Version})
		}
	} else {
		target := sel.Path
		if sel.realPath != "" {
			target = sel.realPath
		}
		rel, err := r.reg.Release(target, sel.Version)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: sel.Path, Reason: err.Error()})
			return
		}
		deps = rel.Requires
	}

	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Path == deps[j].Path {
			return deps[i].Version < deps[j].Version
		}
		return deps[i].Path < deps[j].Path
	})
	for _, d := range deps {
		r.edges[sel.Path] = append(r.edges[sel.Path], d.Path)
		kind, err := manifest.ClassifyVersion(d.Version)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "malformed", Module: d.Path,
				Reason: fmt.Sprintf("来自 %s 的传递依赖版本写法非法：%v", from, err)})
			continue
		}
		c := reqConstraint{path: d.Path, raw: d.Version, kind: kind, source: from, fromSel: sel.Path}
		r.resolveDynamic(&c)
		r.reqs[d.Path] = append(r.reqs[d.Path], c)
	}
}

func (r *Resolver) resolveDynamic(c *reqConstraint) {
	switch c.kind {
	case manifest.KindExact:
		c.exact = c.raw
	case manifest.KindBranch:
		branch := strings.TrimPrefix(c.raw, "@")
		ver, err := r.reg.ResolveBranch(c.path, branch)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: c.path, Reason: err.Error()})
			return
		}
		c.exact = ver
		c.raw = c.raw + " (=" + ver + ")"
	case manifest.KindLatest:
		ver, err := r.reg.Latest(c.path)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: c.path, Reason: err.Error()})
			return
		}
		c.exact = ver
		c.raw = "latest (=" + ver + ")"
	}
}

// detectCycles 对最终依赖图做 DFS，任何回边都标记为循环依赖冲突。
func (r *Resolver) detectCycles() {
	color := map[string]int{} // 0 白 1 灰 2 黑
	var stack []string
	var cycles [][]string
	var dfs func(string)
	dfs = func(u string) {
		color[u] = 1
		stack = append(stack, u)
		for _, v := range r.edges[u] {
			if color[v] == 1 {
				idx := 0
				for i, n := range stack {
					if n == v {
						idx = i
						break
					}
				}
				cyc := append([]string{}, stack[idx:]...)
				cyc = append(cyc, v)
				cycles = append(cycles, cyc)
			} else if color[v] == 0 {
				dfs(v)
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = 2
	}

	nodes := make([]string, 0, len(r.sel))
	for n := range r.sel {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if color[n] == 0 {
			dfs(n)
		}
	}

	seenCyc := map[string]bool{}
	for _, cyc := range cycles {
		key := canonicalCycle(cyc)
		if seenCyc[key] {
			continue
		}
		seenCyc[key] = true
		r.addIssue(Issue{Type: IssueConflict, Kind: "cycle", Module: cyc[0],
			Reason: "依赖图存在循环：" + strings.Join(cyc, " -> ")})
	}
}
