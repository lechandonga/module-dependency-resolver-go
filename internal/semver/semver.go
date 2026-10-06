// Package semver 实现工具内部使用的语义化版本、伪版本与版本区间比较。
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version 是解析后的语义化版本。Pseudo 标记 Go 风格伪版本，如
// v0.0.0-20240101000000-abcdef123456。
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease []Ident
	Pseudo     bool
	Raw        string
}

// Ident 是预发布段落的一个标识符，可能是数字或字符串。
type Ident struct {
	Num uint64
	Str string
	IsN bool
}

func (i Ident) String() string { return i.Str }

var (
	versionRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	pseudoRE  = regexp.MustCompile(`^v[01]\.0\.0-\d{14}-[0-9a-f]{12}$`)
)

// Parse 解析精确版本字符串（含预发布版本与伪版本）。
func Parse(s string) (Version, error) {
	if !versionRE.MatchString(s) {
		return Version{}, fmt.Errorf("版本 %q 不符合 v主.次.修订[-预发布] 语法", s)
	}
	m := versionRE.FindStringSubmatch(s)
	v := Version{Raw: s, Pseudo: pseudoRE.MatchString(s)}
	v.Major, _ = strconv.ParseUint(m[1], 10, 64)
	v.Minor, _ = strconv.ParseUint(m[2], 10, 64)
	v.Patch, _ = strconv.ParseUint(m[3], 10, 64)
	if m[4] != "" {
		for _, seg := range strings.Split(m[4], ".") {
			if seg == "" {
				return Version{}, fmt.Errorf("版本 %q 的预发布段落为空", s)
			}
			if n, err := strconv.ParseUint(seg, 10, 64); err == nil && seg == strconv.FormatUint(n, 10) {
				v.Prerelease = append(v.Prerelease, Ident{Num: n, IsN: true, Str: seg})
			} else {
				v.Prerelease = append(v.Prerelease, Ident{Str: seg})
			}
		}
	}
	return v, nil
}

// String 返回规范化版本字符串（始终带 v 前缀）。
func (v Version) String() string {
	b := strings.Builder{}
	fmt.Fprintf(&b, "v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Prerelease) > 0 {
		segs := make([]string, len(v.Prerelease))
		for i, id := range v.Prerelease {
			segs[i] = id.String()
		}
		b.WriteByte('-')
		b.WriteString(strings.Join(segs, "."))
	}
	return b.String()
}

// IsPrerelease 报告是否为预发布（含伪版本）。
func (v Version) IsPrerelease() bool { return len(v.Prerelease) > 0 }

// Compare 返回 -1/0/1，比较遵循语义化版本规范，数字段落按数值比较。
func Compare(a, b Version) int {
	switch {
	case a.Major != b.Major:
		return boolCmp(a.Major > b.Major)
	case a.Minor != b.Minor:
		return boolCmp(a.Minor > b.Minor)
	case a.Patch != b.Patch:
		return boolCmp(a.Patch > b.Patch)
	}
	return comparePre(a.Prerelease, b.Prerelease)
}

func boolCmp(greater bool) int {
	if greater {
		return 1
	}
	return -1
}

// comparePre 按 semver 规则比较预发布：无预发布 > 有预发布；
// 数字标识符数值更小者更低；数字标识符低于字符串标识符。
func comparePre(a, b []Ident) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		switch {
		case x.IsN && y.IsN && x.Num != y.Num:
			return boolCmp(x.Num > y.Num)
		case x.IsN != y.IsN:
			if x.IsN {
				return -1
			}
			return 1
		case x.Str != y.Str:
			if x.Str < y.Str {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}
