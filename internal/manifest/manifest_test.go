package manifest

import (
	"strings"
	"testing"
)

func parseString(t *testing.T, src string) (*Manifest, []error) {
	t.Helper()
	return Parse(strings.NewReader(src), "testdata/modules.txt")
}

func TestParseAllForms(t *testing.T) {
	// 业务：多种清单写法（单行/括号块、注释、引号、四类版本写法）正确归一。
	src := `
// 头部注释
module example.com/app

require example.com/a v1.2.3
require example.com/b ">=1.0.0 <2.0.0"
require (
	example.com/c @main
	example.com/d latest
	example.com/e v0.0.0-20240101000000-abcdef123456
)

exclude example.com/a v1.2.4

replace example.com/f v1.0.0 => example.com/f v0.9.0
replace example.com/g => ./local/g
`
	m, errs := parseString(t, src)
	if len(errs) != 0 {
		t.Fatalf("不应有解析错误: %v", errs)
	}
	if m.Module != "example.com/app" {
		t.Fatalf("module=%s", m.Module)
	}
	kinds := map[string]VersionKind{}
	for _, q := range m.Requires {
		kinds[q.Path] = q.Kind
	}
	want := map[string]VersionKind{
		"example.com/a": KindExact,
		"example.com/b": KindRange,
		"example.com/c": KindBranch,
		"example.com/d": KindLatest,
		"example.com/e": KindExact,
	}
	for p, k := range want {
		if kinds[p] != k {
			t.Errorf("%s 类别=%s 期望 %s", p, kinds[p], k)
		}
	}
	if len(m.Excludes) != 1 || m.Excludes[0].Version != "v1.2.4" {
		t.Errorf("exclude 解析错误: %+v", m.Excludes)
	}
	if len(m.Replaces) != 2 || m.Replaces[1].LocalPath != "./local/g" {
		t.Errorf("replace 解析错误: %+v", m.Replaces)
	}
}

func TestMalformedManifestReportsLineAndReason(t *testing.T) {
	// 业务：每条畸形声明都要能看出“在哪一行、因为什么”，不得静默跳过。
	src := `module example.com/app
require example.com/a
require example.com/b ~bad-range
require example.com/c @
exclude example.com/d >=1.0.0
replace example.com/e
frobnicate x
module example.com/other
require (
 example.com/f v1.0.0
`
	_, errs := parseString(t, src)
	if len(errs) < 6 {
		t.Fatalf("应至少收集 6 条错误，实际 %d: %v", len(errs), errs)
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	for _, want := range []string{
		"testdata/modules.txt:2",
		"版本写法 \"~bad-range\"",
		"分支引用 \"@\"",
		"exclude 只允许精确版本",
		"replace 缺少 => 分隔符",
		"未知指令 \"frobnicate\"",
		"module 指令重复声明",
		"括号块缺少结束符",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("错误信息中缺少 %q；全部错误:\n%s", want, joined)
		}
	}
}

func TestReplaceRejectsBadShape(t *testing.T) {
	// 业务：replace 两侧字段缺失、版本非精确都要按原因报错。
	src := `module m
replace example.com/a v1.0.0 => example.com/b
replace example.com/b vx => example.com/b v1.0.0
replace example.com/c => example.com/c latest
`
	_, errs := parseString(t, src)
	if len(errs) != 3 {
		t.Fatalf("期望 3 条 replace 错误，实际 %d: %v", len(errs), errs)
	}
}

func TestMissingModuleDirective(t *testing.T) {
	_, errs := parseString(t, "require a v1.0.0\n")
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "缺少 module") {
		t.Fatalf("应报缺少 module: %v", errs)
	}
}
