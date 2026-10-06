package resolver

import (
	"strings"
	"testing"

	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
)

func TestTransitiveMVSAndRanges(t *testing.T) {
	// 业务：传递依赖展开 + 多来源区间合并，选最高共同满足版本；结果确定。
	w := newWorld(t).
		mod("a",
			registry.Release{Version: "v1.0.0", Digest: "sha256:a10", Requires: []registry.RequireRef{{Path: "c", Version: ">=1.0.0 <2.0.0"}}},
			registry.Release{Version: "v1.1.0", Digest: "sha256:a11", Requires: []registry.RequireRef{{Path: "c", Version: ">=1.2.0"}}},
		).
		mod("b",
			registry.Release{Version: "v1.0.0", Digest: "sha256:b10", Requires: []registry.RequireRef{{Path: "c", Version: "<=1.5.0"}}},
		).
		mod("c",
			registry.Release{Version: "v1.0.0", Digest: "sha256:c10"},
			registry.Release{Version: "v1.2.0", Digest: "sha256:c12"},
			registry.Release{Version: "v1.5.0", Digest: "sha256:c15"},
			registry.Release{Version: "v2.0.0", Digest: "sha256:c20"},
		)
	res := w.resolve("module app\nrequire a >=1.0.0\nrequire b v1.0.0\n")
	if len(res.Issues) != 0 {
		t.Fatalf("不应有冲突: %v", res.Issues)
	}
	if got := selVersion(res, "a"); got != "v1.1.0" {
		t.Errorf("a 应选最高 v1.1.0，实际 %s", got)
	}
	if got := selVersion(res, "c"); got != "v1.5.0" {
		t.Errorf("c 应选区间交集最高 v1.5.0，实际 %s", got)
	}
}

func TestRangeNoIntersectionIsConflict(t *testing.T) {
	// 业务：区间无交集必须明确报冲突，不允许随手挑一个版本。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: ">=2.0.0"}}}).
		mod("b", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: "<2.0.0"}}}).
		mod("c",
			registry.Release{Version: "v1.9.0", Digest: "x"},
			registry.Release{Version: "v2.0.0", Digest: "x"},
		)
	res := w.resolve("module app\nrequire a v1.0.0\nrequire b v1.0.0\n")
	if !hasIssue(res, "range", "c") {
		t.Fatalf("应报 c 的区间冲突: %v", res.Issues)
	}
}

func TestDuplicateExactVersionsIsConflict(t *testing.T) {
	// 业务：同一模块被不同来源钉到不同精确版本 => 冲突而非任选其一。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: "v1.0.0"}}}).
		mod("c",
			registry.Release{Version: "v1.0.0", Digest: "x"},
			registry.Release{Version: "v2.0.0", Digest: "x"},
		)
	res := w.resolve("module app\nrequire a v1.0.0\nrequire c v2.0.0\n")
	if !hasIssue(res, "exact", "c") {
		t.Fatalf("应报 c 的精确版本冲突: %v", res.Issues)
	}
}

func TestExactVersusIncompatibleRangeIsConflict(t *testing.T) {
	// 业务：精确 pin 与区间不相容 => 冲突。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: "v1.0.0"}}}).
		mod("c",
			registry.Release{Version: "v1.0.0", Digest: "x"},
			registry.Release{Version: "v1.1.0", Digest: "x"},
		)
	res := w.resolve("module app\nrequire a v1.0.0\nrequire c >=1.1.0\n")
	if !hasIssue(res, "range", "c") {
		t.Fatalf("应报 pin 与区间冲突: %v", res.Issues)
	}
}

func TestCycleIsConflict(t *testing.T) {
	// 业务：循环依赖必须标记成冲突并给出完整环路径。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "b", Version: "v1.0.0"}}}).
		mod("b", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "a", Version: "v1.0.0"}}})
	res := w.resolve("module app\nrequire a v1.0.0\n")
	found := false
	for _, is := range res.Issues {
		if is.Kind == "cycle" && strings.Contains(is.Reason, "a -> b -> a") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应报 a/b 循环: %v", res.Issues)
	}
}

func TestExcludedVersionIsConflict(t *testing.T) {
	// 业务：唯一可用版本被 exclude => 冲突。
	w := newWorld(t).mod("a", registry.Release{Version: "v1.0.0", Digest: "x"})
	res := w.resolve("module app\nrequire a v1.0.0\nexclude a v1.0.0\n")
	if !hasIssue(res, "excluded", "a") {
		t.Fatalf("应报 exclude 冲突: %v", res.Issues)
	}
}

func TestBranchAndLatestResolution(t *testing.T) {
	// 业务：分支与 latest 在解析阶段确定为具体版本，参与后续选择且结果确定。
	w := newWorld(t).
		mod("a",
			registry.Release{Version: "v1.0.0", Digest: "x"},
			registry.Release{Version: "v1.1.0-pre.1", Digest: "y"},
		).
		branch("a", "main", "v1.1.0-pre.1").
		mod("b",
			registry.Release{Version: "v0.9.0", Digest: "x"},
			registry.Release{Version: "v1.0.0", Digest: "y"},
		)
	res := w.resolve("module app\nrequire a @main\nrequire b latest\n")
	if len(res.Issues) != 0 {
		t.Fatalf("不应有冲突: %v", res.Issues)
	}
	if got := selVersion(res, "a"); got != "v1.1.0-pre.1" {
		t.Errorf("a 分支 main 应解析到 v1.1.0-pre.1，实际 %s", got)
	}
	if got := selVersion(res, "b"); got != "v1.0.0" {
		t.Errorf("b latest 应取最高稳定版 v1.0.0，实际 %s", got)
	}
}

func TestUnknownBranchAndModuleAreErrors(t *testing.T) {
	// 业务：分支不存在、模块不存在属于数据错误，不能被静默忽略。
	w := newWorld(t).mod("a", registry.Release{Version: "v1.0.0", Digest: "x"})
	res := w.resolve("module app\nrequire a @nope\nrequire ghost v1.0.0\n")
	if !hasIssue(res, "missing", "a") || !hasIssue(res, "missing", "ghost") {
		t.Fatalf("应分别报 a/ghost 缺失: %v", res.Issues)
	}
}

func TestReplaceRemoteVersionAndChain(t *testing.T) {
	// 业务：指定版本 replace 满足精确 pin，取替换目标的摘要与依赖。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: "v2.0.0"}}}).
		mod("c",
			registry.Release{Version: "v2.0.0", Digest: "new"},
			registry.Release{Version: "v1.0.0", Digest: "old"},
		)
	res := w.resolve("module app\nrequire a v1.0.0\nreplace c v2.0.0 => c v1.0.0\n")
	if len(res.Issues) != 0 {
		t.Fatalf("合法降级 replace 不应冲突: %v", res.Issues)
	}
	if got := selVersion(res, "c"); got != "v1.0.0" {
		t.Errorf("c 应被替换为 v1.0.0，实际 %s", got)
	}
	if res.Selections["c"].Digest != "old" {
		t.Errorf("应取替换目标的内容摘要 old")
	}
}

func TestReplaceLoopIsConflict(t *testing.T) {
	// 业务：replace 链路成环（自相矛盾）必须标记。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x"}).
		mod("b", registry.Release{Version: "v1.0.0", Digest: "x"}).
		mod("c", registry.Release{Version: "v1.0.0", Digest: "x"})
	res := w.resolve("module app\nrequire a v1.0.0\nreplace a => b v1.0.0\nreplace b => c v1.0.0\nreplace c => a v1.0.0\n")
	if !hasIssue(res, "replace", "") {
		t.Fatalf("应报 replace 成环冲突: %v", res.Issues)
	}
}

func TestDuplicateContradictoryReplaceIsConflict(t *testing.T) {
	// 业务：同一模块两条相互矛盾的 replace 指令 => 冲突。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x"}).
		mod("b", registry.Release{Version: "v1.0.0", Digest: "x"}).
		mod("c", registry.Release{Version: "v1.0.0", Digest: "x"})
	res := w.resolve("module app\nrequire a v1.0.0\nreplace a => b v1.0.0\nreplace a => c v1.0.0\n")
	if !hasIssue(res, "replace", "a") {
		t.Fatalf("应报 a 的矛盾 replace: %v", res.Issues)
	}
}

func TestLocalReplaceHashesDirectoryAndLoadsItsDeps(t *testing.T) {
	// 业务：本地 replace 以目录内容摘要锁定，其 modules.txt 里的依赖继续参与展开。
	w := newWorld(t).
		mod("dep", registry.Release{Version: "v1.0.0", Digest: "sha256:dep1"})
	rel := w.localModule("local-a", "module local/a\nrequire dep v1.0.0\n")
	res := w.resolve("module app\nrequire a v0.0.0-local\nreplace a => " + rel + "\n")
	if len(res.Issues) != 0 {
		t.Fatalf("本地 replace 不应冲突: %v", res.Issues)
	}
	s := res.Selections["a"]
	if s == nil || !s.Local || s.Digest == "" {
		t.Fatalf("a 应为带摘要的本地替换: %+v", s)
	}
	if got := selVersion(res, "dep"); got != "v1.0.0" {
		t.Errorf("本地模块的传递依赖应被展开，实际 dep=%s", got)
	}
}

func TestDeterministicAcrossRuns(t *testing.T) {
	// 业务：同一份输入多次解析，选择与解释必须完全一致。
	w := newWorld(t).
		mod("a", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: ">=1.0.0"}}}).
		mod("b", registry.Release{Version: "v1.0.0", Digest: "x", Requires: []registry.RequireRef{{Path: "c", Version: "<2.0.0"}}}).
		mod("c",
			registry.Release{Version: "v1.0.0", Digest: "x"},
			registry.Release{Version: "v1.5.0", Digest: "y"},
			registry.Release{Version: "v2.0.0", Digest: "z"},
		)
	src := "module app\nrequire a v1.0.0\nrequire b v1.0.0\n"
	first := w.resolve(src)
	for i := 0; i < 5; i++ {
		again := w.resolve(src)
		if got, want := selVersion(again, "c"), selVersion(first, "c"); got != want {
			t.Fatalf("第 %d 次解析结果漂移：%s != %s", i+2, got, want)
		}
	}
}
