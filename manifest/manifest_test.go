package manifest

import (
	"strings"
	"testing"
)

// TestParseValid covers business: 清单归一 —— 精确版本、区间、伪版本、分支
// 引用与 require/exclude/replace 指令的解析。
func TestParseValid(t *testing.T) {
	t.Log("业务覆盖: 清单归一化 — 精确版本/区间/伪版本/分支引用/replace/exclude")
	cases := []struct {
		name string
		json string
		want func(t *testing.T, m *Manifest)
	}{
		{
			name: "exact version",
			json: `{"module":"app","requires":[{"path":"a","version":"v1.2.3"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if m.Requires[0].Constraint.String() != "v1.2.3" {
					t.Fatalf("got %s", m.Requires[0].Constraint)
				}
			},
		},
		{
			name: "comparator range",
			json: `{"module":"app","requires":[{"path":"a","version":">=1.0.0 <2.0.0"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if m.Requires[0].Constraint.String() != ">=1.0.0 <2.0.0" {
					t.Fatalf("got %s", m.Requires[0].Constraint)
				}
			},
		},
		{
			name: "interval range",
			json: `{"module":"app","requires":[{"path":"a","version":"[1.0.0,2.0.0)"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if m.Requires[0].Constraint.String() != "[1.0.0,2.0.0)" {
					t.Fatalf("got %s", m.Requires[0].Constraint)
				}
			},
		},
		{
			name: "pseudo version",
			json: `{"module":"app","requires":[{"path":"a","version":"v0.0.0-20240102030405-abcdef123456"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if m.Requires[0].Constraint.String() != "v0.0.0-20240102030405-abcdef123456" {
					t.Fatalf("got %s", m.Requires[0].Constraint)
				}
			},
		},
		{
			name: "branch ref",
			json: `{"module":"app","requires":[{"path":"a","ref":"branch:main"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if m.Requires[0].Branch != "main" {
					t.Fatalf("got branch %q", m.Requires[0].Branch)
				}
			},
		},
		{
			name: "exclude and replace",
			json: `{"module":"app","requires":[{"path":"a","version":"v1.0.0"}],
			        "excludes":[{"path":"a","version":"v1.1.0"}],
			        "replaces":[{"path":"a","with":"b","version":"v2.0.0"}]}`,
			want: func(t *testing.T, m *Manifest) {
				if len(m.Excludes["a"]) != 1 || m.Replaces[0].With != "b" {
					t.Fatalf("got %+v %+v", m.Excludes, m.Replaces)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse([]byte(tc.json))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.want(t, m)
			t.Logf("用例 %q 通过: 归一化结果符合预期", tc.name)
		})
	}
}

// TestParseMalformed covers business: 畸形清单 —— 语法坏、区间非法、缺字段
// 必须按原因分类报错并定位到具体条目。
func TestParseMalformed(t *testing.T) {
	t.Log("业务覆盖: 畸形清单 — 语法错误/非法区间/缺字段/重复声明的分类报错")
	cases := []struct {
		name     string
		json     string
		wantKind ErrorKind
		wantLoc  string
	}{
		{"broken json", `{"module":`, ErrSyntax, "manifest"},
		{"unknown top-level field", `{"module":"app","requirex":[]}`, ErrSyntax, "manifest"},
		{"missing module", `{"requires":[]}`, ErrMissingField, "module"},
		{"require missing path", `{"module":"app","requires":[{"version":"v1.0.0"}]}`, ErrMissingField, "require[0]"},
		{"require missing version and ref", `{"module":"app","requires":[{"path":"a"}]}`, ErrMissingField, "require[0](a)"},
		{"require both version and ref", `{"module":"app","requires":[{"path":"a","version":"v1.0.0","ref":"branch:main"}]}`, ErrUnknownField, "require[0](a)"},
		{"bad exact version", `{"module":"app","requires":[{"path":"a","version":"1.0"}]}`, ErrInvalidVer, "require[0](a)"},
		{"bad pseudo version", `{"module":"app","requires":[{"path":"a","version":"v1.0.0-rc1"}]}`, ErrInvalidVer, "require[0](a)"},
		{"empty interval", `{"module":"app","requires":[{"path":"a","version":"[2.0.0,1.0.0]"}]}`, ErrInvalidRange, "require[0](a)"},
		{"malformed interval", `{"module":"app","requires":[{"path":"a","version":"[1.0.0]"}]}`, ErrInvalidRange, "require[0](a)"},
		{"bad comparator range", `{"module":"app","requires":[{"path":"a","version":">=2.0.0 <1.0.0"}]}`, ErrInvalidRange, "require[0](a)"},
		{"unsupported ref", `{"module":"app","requires":[{"path":"a","ref":"tag:v1"}]}`, ErrUnknownField, "require[0](a)"},
		{"duplicate require", `{"module":"app","requires":[{"path":"a","version":"v1.0.0"},{"path":"a","version":"v2.0.0"}]}`, ErrDuplicateDecl, "require[1](a)"},
		{"exclude missing version", `{"module":"app","requires":[],"excludes":[{"path":"a"}]}`, ErrMissingField, "exclude[0](a)"},
		{"exclude bad version", `{"module":"app","requires":[],"excludes":[{"path":"a","version":"x"}]}`, ErrInvalidVer, "exclude[0](a)"},
		{"replace missing with", `{"module":"app","requires":[],"replaces":[{"path":"a"}]}`, ErrMissingField, "replace[0](a)"},
		{"replace bad version", `{"module":"app","requires":[],"replaces":[{"path":"a","with":"b","version":"nope"}]}`, ErrInvalidVer, "replace[0](a)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var found *Error
			switch e := err.(type) {
			case Errors:
				for _, one := range e {
					if one.Kind == tc.wantKind {
						found = one
					}
				}
			case *Error:
				found = e
			}
			if found == nil {
				t.Fatalf("expected kind %s, got: %v", tc.wantKind, err)
			}
			loc := found.Directive
			if found.Index >= 0 {
				loc = loc + "[" + itoa(found.Index) + "]"
			}
			if found.Path != "" {
				loc += "(" + found.Path + ")"
			}
			if loc != tc.wantLoc {
				t.Fatalf("expected location %q, got %q", tc.wantLoc, loc)
			}
			t.Logf("用例 %q 通过: 错误定位=%s 原因=%s", tc.name, loc, found.Kind)
		})
	}
}

// TestParseCollectsAllErrors covers business: 多条坏条目一次性全部报出，
// 而不是遇到第一条就停止。
func TestParseCollectsAllErrors(t *testing.T) {
	t.Log("业务覆盖: 畸形清单 — 一次报出全部坏条目，不静默跳过")
	_, err := Parse([]byte(`{"module":"app","requires":[{"path":"a"},{"version":"v1.0.0"}],"excludes":[{"path":"b"}]}`))
	es, ok := err.(Errors)
	if !ok {
		t.Fatalf("expected Errors, got %T: %v", err, err)
	}
	if len(es) != 3 {
		t.Fatalf("expected 3 errors, got %d: %v", len(es), es)
	}
	if !strings.Contains(es.Error(), "require[0](a)") || !strings.Contains(es.Error(), "exclude[0](b)") {
		t.Fatalf("error text should locate entries: %v", es)
	}
	t.Logf("聚合报错通过: 3 条坏声明全部定位 -> %v", es)
}

func itoa(i int) string {
	return string(rune('0' + i))
}
