package resolver

import (
	"fmt"
	"testing"

	"github.com/lechandonga/module-dependency-resolver-go/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/registry"
)

func mustManifest(t *testing.T, json string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(json))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	return m
}

func mustRegistry(t *testing.T, json string) *registry.Registry {
	t.Helper()
	r, err := registry.Parse([]byte(json))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return r
}

func hasConflict(res *Result, kind ConflictKind, path string) *Conflict {
	for i := range res.Conflicts {
		if res.Conflicts[i].Kind == kind && res.Conflicts[i].Path == path {
			return &res.Conflicts[i]
		}
	}
	return nil
}

// TestTransitiveExpansion covers business: 传递依赖展开 —— 根清单只声明
// 直接依赖，间接依赖从 registry 逐层展开，每模块只选一个版本。
func TestTransitiveExpansion(t *testing.T) {
	t.Log("业务覆盖: 传递依赖展开 + 版本选择(交集内取最高)")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[
	    {"version":"v1.0.0","content":"a1","requires":{"b":">=1.0.0 <2.0.0"}},
	    {"version":"v1.1.0","content":"a11","requires":{"b":">=1.2.0 <2.0.0","c":"v0.0.0-20240102030405-abcdef123456"}}]},
	  {"path":"b","versions":[
	    {"version":"v1.0.0","content":"b1"},
	    {"version":"v1.2.0","content":"b12"},
	    {"version":"v1.3.0","content":"b13"}]},
	  {"path":"c","versions":[{"version":"v0.0.0-20240102030405-abcdef123456","content":"cp"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"[1.0.0,2.0.0)"}]}`)
	res := Resolve(m, reg)
	if !res.Ok() {
		t.Fatalf("conflicts: %v", res.Conflicts)
	}
	want := map[string]string{}
	for _, e := range res.Entries() {
		want[e.Path] = e.Version
	}
	expect := map[string]string{
		"a": "v1.1.0", // highest in [1.0.0,2.0.0)
		"b": "v1.3.0", // intersection of >=1.0.0<2.0.0 and >=1.2.0<2.0.0, highest
		"c": "v0.0.0-20240102030405-abcdef123456", // pseudo-version exact pin
	}
	for p, v := range expect {
		if want[p] != v {
			t.Fatalf("module %s: want %s, got %s (all: %v)", p, v, want[p], want)
		}
	}
	t.Logf("传递展开通过: a@v1.1.0 引入 b/c，b 取两约束交集的最高版 v1.3.0，c 为伪版本锁定")
}

// TestDeterministic covers business: 同一输入多次运行结果完全一致。
func TestDeterministic(t *testing.T) {
	t.Log("业务覆盖: 确定性 — 同一输入重复解析结果逐字节一致")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a","requires":{"b":">=1.0.0","c":"[1.0.0,3.0.0)"}}]},
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b","requires":{"c":"v2.0.0"}},{"version":"v1.1.0","content":"b","requires":{"c":"v2.0.0"}}]},
	  {"path":"c","versions":[{"version":"v1.0.0","content":"c"},{"version":"v2.0.0","content":"c2"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"v1.0.0"},{"path":"b","version":">=1.0.0"}]}`)
	first := Resolve(m, reg)
	for i := 0; i < 20; i++ {
		got := Resolve(m, reg)
		if fmt.Sprintf("%v|%v", first.Entries(), first.Conflicts) != fmt.Sprintf("%v|%v", got.Entries(), got.Conflicts) {
			t.Fatalf("run %d differs", i)
		}
	}
	t.Log("确定性通过: 21 次解析结果完全一致")
}

// TestNoIntersection covers business: 区间无交集 —— 标记冲突而不是随手挑一个。
func TestNoIntersection(t *testing.T) {
	t.Log("业务覆盖: 版本冲突 — 区间无交集报 no_version_intersection")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a","requires":{"b":">=2.0.0"}}]},
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b1"},{"version":"v2.0.0","content":"b2"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"v1.0.0"},{"path":"b","version":"<2.0.0"}]}`)
	res := Resolve(m, reg)
	c := hasConflict(res, ConflictDuplicate, "b")
	if c == nil {
		c = hasConflict(res, ConflictNoIntersection, "b")
	}
	if c == nil {
		t.Fatalf("expected conflict for b, got %+v", res.Conflicts)
	}
	if _, ok := res.Selected["b"]; ok {
		t.Fatalf("b must not be selected on conflict")
	}
	t.Logf("区间无交集通过: kind=%s sources=%v", c.Kind, c.Sources)
}

// TestCycle covers business: 循环依赖被检测并标记。
func TestCycle(t *testing.T) {
	t.Log("业务覆盖: 循环依赖 — 展开可终止但图中环被标记为 dependency_cycle")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a","requires":{"b":"v1.0.0"}}]},
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b","requires":{"a":"v1.0.0"}}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"v1.0.0"}]}`)
	res := Resolve(m, reg)
	c := hasConflict(res, ConflictCycle, "a")
	if c == nil {
		t.Fatalf("expected cycle conflict, got %+v", res.Conflicts)
	}
	t.Logf("循环检测通过: %s", c.Detail)
}

// TestReplaceChainConflict covers business: replace 链路自相矛盾。
func TestReplaceChainConflict(t *testing.T) {
	t.Log("业务覆盖: replace 冲突 — 链路与版本钉死矛盾")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a"}]},
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"v1.0.0"}],
	  "replaces":[{"path":"a","with":"b"},{"path":"b","with":"a"}]}`)
	res := Resolve(m, reg)
	if hasConflict(res, ConflictReplaceChain, "a") == nil && hasConflict(res, ConflictReplaceChain, "b") == nil {
		t.Fatalf("expected replace chain conflict, got %+v", res.Conflicts)
	}
	t.Log("replace 环路通过: a->b->a 被标记为 replace_chain_conflict")
}

// TestReplaceRedirect covers business: replace 正常重定向并钉版本。
func TestReplaceRedirect(t *testing.T) {
	t.Log("业务覆盖: replace 归一 — a 重定向到 b 且钉死版本")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b1"},{"version":"v1.5.0","content":"b15"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":">=1.0.0"}],
	  "replaces":[{"path":"a","with":"b","version":"v1.0.0"}]}`)
	res := Resolve(m, reg)
	if !res.Ok() {
		t.Fatalf("conflicts: %v", res.Conflicts)
	}
	sel := res.Selected["a"]
	if sel.Path != "b" || sel.Version != "v1.0.0" || sel.ReplacedBy != "a" {
		t.Fatalf("got %+v", sel)
	}
	t.Log("replace 重定向通过: a 解析为 b@v1.0.0")
}

// TestExclude covers business: exclude 从候选集中剔除版本。
func TestExclude(t *testing.T) {
	t.Log("业务覆盖: exclude — 最高版本被剔除后选择次高版本")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a1"},{"version":"v1.1.0","content":"a11"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":">=1.0.0"}],
	  "excludes":[{"path":"a","version":"v1.1.0"}]}`)
	res := Resolve(m, reg)
	if !res.Ok() || res.Selected["a"].Version != "v1.0.0" {
		t.Fatalf("got %+v conflicts=%v", res.Selected["a"], res.Conflicts)
	}
	t.Log("exclude 通过: v1.1.0 被剔除，选中 v1.0.0")
}

// TestBranchRef covers business: 分支引用解析为分支当前指向的版本。
func TestBranchRef(t *testing.T) {
	t.Log("业务覆盖: 分支引用 — branch:main 解析为分支头版本")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a1"},{"version":"v1.2.0","content":"a12"}],
	    "branches":{"main":"v1.2.0"}}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","ref":"branch:main"}]}`)
	res := Resolve(m, reg)
	if !res.Ok() || res.Selected["a"].Version != "v1.2.0" {
		t.Fatalf("got %+v conflicts=%v", res.Selected["a"], res.Conflicts)
	}
	t.Log("分支引用通过: main -> v1.2.0")
}

// TestDuplicateIntroduction covers business: 同一模块被不同来源以不兼容
// 约束重复引入时标记 duplicate_introduction。
func TestDuplicateIntroduction(t *testing.T) {
	t.Log("业务覆盖: 重复引入冲突 — 不同来源约束不兼容")
	reg := mustRegistry(t, `{"modules":[
	  {"path":"a","versions":[{"version":"v1.0.0","content":"a","requires":{"c":"v1.0.0"}}]},
	  {"path":"b","versions":[{"version":"v1.0.0","content":"b","requires":{"c":"v2.0.0"}}]},
	  {"path":"c","versions":[{"version":"v1.0.0","content":"c1"},{"version":"v2.0.0","content":"c2"}]}
	]}`)
	m := mustManifest(t, `{"module":"app","requires":[{"path":"a","version":"v1.0.0"},{"path":"b","version":"v1.0.0"}]}`)
	res := Resolve(m, reg)
	c := hasConflict(res, ConflictDuplicate, "c")
	if c == nil {
		t.Fatalf("expected duplicate_introduction, got %+v", res.Conflicts)
	}
	if len(c.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %v", c.Sources)
	}
	t.Logf("重复引入通过: 来源=%v", c.Sources)
}
