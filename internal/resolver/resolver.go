// Package resolver 在本地清单与注册数据之上做传递依赖展开、确定性版本选择、
// 冲突标记，产出可解释的解析结论。
package resolver

import (
	"fmt"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
)

// IssueType 区分“冲突”（声明互相矛盾）与“错误”（环境/数据缺失等）。
type IssueType string

const (
	IssueConflict IssueType = "conflict"
	IssueError    IssueType = "error"
)

// Issue 是一条带分类、模块与原因的解析结论。
type Issue struct {
	Type   IssueType
	Kind   string // range / exact / cycle / replace / excluded / missing / malformed
	Module string
	Reason string
}

func (i Issue) Error() string {
	return fmt.Sprintf("[%s/%s] %s: %s", i.Type, i.Kind, i.Module, i.Reason)
}

// Contribution 记录某条版本约束来自哪里，用于解释最终选择。
type Contribution struct {
	Source string // 例如 "根清单" 或 "example.com/a v1.0.0"
	Raw    string // 原始版本写法
	Kind   manifest.VersionKind
}

// Selection 是某模块的最终选中结果。
type Selection struct {
	Path         string
	Version      string
	Digest       string
	LocalPath    string
	Local        bool
	ReplacedFrom string // 形如 "example.com/x v1.0.0" 或 "example.com/x（全版本）"
	Contributors []Contribution
	realPath     string // 远程 replace 时实际取数的模块路径（为空则等于 Path）
}

// Result 是解析结论。
type Result struct {
	Root       string
	Selections map[string]*Selection
	Issues     []Issue
}

type reqConstraint struct {
	path    string
	raw     string
	kind    manifest.VersionKind
	source  string
	exact   string // branch/latest 在收集时解析成精确版本
	fromSel string // 传递依赖来源模块（根清单为 ""）
}

type replKey struct{ path, version string }

type replTarget struct {
	newPath    string
	newVersion string
	localPath  string
	line       int
	oldVersion string // 非空表示“指定版本”replace
}

// Resolver 持有一次解析的全部上下文。
type Resolver struct {
	root     *manifest.Manifest
	reg      *registry.Registry
	rootDir  string
	repl     map[replKey]replTarget
	reqs     map[string][]reqConstraint
	excludes map[string]map[string]bool
	sel      map[string]*Selection
	issues   []Issue
	loaded   map[string]bool
	edges    map[string][]string
	issueSet map[string]bool
}

// New 构造解析器。rootDir 用于解析 replace 的相对本地目录。
func New(root *manifest.Manifest, reg *registry.Registry, rootDir string) *Resolver {
	return &Resolver{
		root:     root,
		reg:      reg,
		rootDir:  rootDir,
		repl:     map[replKey]replTarget{},
		reqs:     map[string][]reqConstraint{},
		excludes: map[string]map[string]bool{},
		sel:      map[string]*Selection{},
		loaded:   map[string]bool{},
		edges:    map[string][]string{},
		issueSet: map[string]bool{},
	}
}

// Resolve 执行解析。返回的 Result 即使带 Issue 也尽量包含已得出的选择，
// 便于解释与定位；调用方应以 Issues 是否为空判断成功与否。
func (r *Resolver) Resolve() *Result {
	r.collectExcludes()
	r.collectReplaces()

	for _, req := range r.root.Requires {
		r.addRequire(req.Path, req.Version, req.Kind, "根清单")
	}

	r.fixpoint()
	r.detectCycles()

	res := &Result{Root: r.root.Module, Selections: r.sel, Issues: r.issues}
	sortIssues(res.Issues)
	return res
}

func (r *Resolver) collectExcludes() {
	for _, ex := range r.root.Excludes {
		if r.excludes[ex.Path] == nil {
			r.excludes[ex.Path] = map[string]bool{}
		}
		r.excludes[ex.Path][ex.Version] = true
	}
}

func (r *Resolver) collectReplaces() {
	for _, rp := range r.root.Replaces {
		key := replKey{path: rp.OldPath, version: rp.OldVersion}
		t := replTarget{newPath: rp.NewPath, newVersion: rp.NewVersion, localPath: rp.LocalPath, line: rp.Line}
		if old, ok := r.repl[key]; ok {
			if old != t {
				r.addIssue(Issue{
					Type: IssueConflict, Kind: "replace", Module: rp.OldPath,
					Reason: fmt.Sprintf("第 %d 行与第 %d 行对同一模块给出了互相矛盾的 replace 目标", rp.Line, old.line),
				})
			}
			continue
		}
		r.repl[key] = t
	}
	for key := range r.repl {
		if chain, loop := r.followReplace(key.path, key.version); loop {
			r.addIssue(Issue{
				Type: IssueConflict, Kind: "replace", Module: key.path,
				Reason: fmt.Sprintf("replace 链路自相矛盾（成环）：%s", strings.Join(chain, " -> ")),
			})
		}
	}
}

// followReplace 跟随全版本与指定版本两类规则直到终点，返回访问链与是否成环。
func (r *Resolver) followReplace(path, version string) ([]string, bool) {
	chain := []string{path + "@" + version}
	seen := map[string]bool{path + "@" + version: true}
	for i := 0; i < 64; i++ {
		// 沿链下一跳优先用当前节点的固定版本继续匹配（可能命中指定版本规则），
		// 否则回落到该模块的全版本规则。
		t, ok := r.repl[replKey{path: path, version: version}]
		if !ok {
			t, ok = r.repl[replKey{path: path}]
		}
		if !ok || t.localPath != "" {
			return chain, false
		}
		next := t.newPath
		key := next + "@" + t.newVersion
		if seen[key] {
			chain = append(chain, key)
			return chain, true
		}
		seen[key] = true
		chain = append(chain, key)
		path, version = next, t.newVersion
	}
	return chain, true
}

func (r *Resolver) addRequire(path, raw string, kind manifest.VersionKind, source string) {
	c := reqConstraint{path: path, raw: raw, kind: kind, source: source}
	switch kind {
	case manifest.KindExact:
		c.exact = raw
	case manifest.KindBranch:
		branch := strings.TrimPrefix(raw, "@")
		ver, err := r.reg.ResolveBranch(path, branch)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
			return
		}
		c.exact = ver
		c.raw = raw + " (=" + ver + ")"
	case manifest.KindLatest:
		ver, err := r.reg.Latest(path)
		if err != nil {
			r.addIssue(Issue{Type: IssueError, Kind: "missing", Module: path, Reason: err.Error()})
			return
		}
		c.exact = ver
		c.raw = "latest (=" + ver + ")"
	}
	r.reqs[path] = append(r.reqs[path], c)
}
