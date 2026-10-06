// Package manifest 解析模块依赖清单（modules.toml 风格的本地文本格式）。
//
// 支持指令：module、require、exclude、replace，支持单行与括号块两种写法，
// 支持 // 行注释、引号包裹的路径。版本写法支持：
// 精确版本 v1.2.3、版本区间 >=1.0.0 <2.0.0、伪版本、分支引用 @main、latest。
package manifest

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
)

// Manifest 是归一化后的依赖清单。
type Manifest struct {
	Module   string
	Source   string // 来源标识（文件名等），用于报错与解释
	Requires []Require
	Excludes []Exclude
	Replaces []Replace
}

// Require 是一条依赖声明。Version 保留原始版本写法，
// 由 Kind 标明属于精确版本 / 区间 / 分支 / latest。
type Require struct {
	Path    string
	Version string
	Kind    VersionKind
	Line    int
}

// Exclude 排除某模块的某个精确版本。
type Exclude struct {
	Path    string
	Version string
	Line    int
}

// Replace 重定向某模块。OldVersion 为空表示全版本重定向。
// LocalPath 非空表示重定向到本地目录；否则 NewPath/NewVersion 指向另一个模块版本。
type Replace struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
	LocalPath  string
	Line       int
}

// VersionKind 是依赖版本声明的归一化类别。
type VersionKind string

const (
	KindExact  VersionKind = "exact"  // 精确版本（含伪版本）
	KindRange  VersionKind = "range"  // 版本区间
	KindBranch VersionKind = "branch" // 分支引用 @xxx
	KindLatest VersionKind = "latest" // latest
)

// Error 指向清单中某一行的声明级错误。
type Error struct {
	Source string
	Line   int
	Text   string
	Reason string
}

func (e *Error) Error() string {
	if e.Source != "" {
		return fmt.Sprintf("%s:%d: %s（声明：%s）", e.Source, e.Line, e.Reason, strings.TrimSpace(e.Text))
	}
	return fmt.Sprintf("第 %d 行: %s（声明：%s）", e.Line, e.Reason, strings.TrimSpace(e.Text))
}

type errorList []error

func (el errorList) Error() string {
	msgs := make([]string, len(el))
	for i, e := range el {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Parse 从 r 解析清单，source 仅用于报错时定位文件。
func Parse(r io.Reader, source string) (*Manifest, []error) {
	m := &Manifest{Source: source}
	var errs []error
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	// blockDir 非空表示当前处于某个括号块内，值为指令名。
	var blockDir string
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		line := stripComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if blockDir != "" {
			if trimmed == ")" {
				blockDir = ""
				continue
			}
			if strings.HasPrefix(trimmed, ")") {
				errs = append(errs, errAt(source, lineNo, raw, "块结束符 ) 后存在多余内容"))
				blockDir = ""
				continue
			}
			if err := m.parseEntry(blockDir, trimmed, lineNo, raw, source); err != nil {
				errs = append(errs, err)
			}
			continue
		}

		dir, rest, ok := splitDirective(trimmed)
		if !ok {
			errs = append(errs, errAt(source, lineNo, raw, fmt.Sprintf("无法识别的指令 %q，应为 module/require/exclude/replace", firstField(trimmed))))
			continue
		}

		if strings.HasSuffix(rest, "(") && strings.TrimSpace(rest[:len(rest)-1]) == "" {
			if dir != "require" && dir != "exclude" && dir != "replace" {
				errs = append(errs, errAt(source, lineNo, raw, fmt.Sprintf("指令 %s 不支持括号块", dir)))
				continue
			}
			blockDir = dir
			continue
		}
		if err := m.parseEntry(dir, rest, lineNo, raw, source); err != nil {
			errs = append(errs, err)
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, fmt.Errorf("读取清单 %s 失败：%w", source, err))
	}
	if blockDir != "" {
		errs = append(errs, errAt(source, lineNo, "", fmt.Sprintf("指令 %s 的括号块缺少结束符 )", blockDir)))
	}
	if m.Module == "" && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("清单 %s 缺少 module 指令", source))
	}
	if m.Module == "" {
		// module 缺失已经追加过错误；保留其它错误一起返回。
	}
	return m, errs
}

func errAt(source string, line int, text, reason string) error {
	return &Error{Source: source, Line: line, Text: text, Reason: reason}
}

func (m *Manifest) parseEntry(dir, rest string, line int, raw, source string) error {
	switch dir {
	case "module":
		path, extra, err := oneToken(rest)
		if err != nil {
			return errAt(source, line, raw, "module 指令缺少模块路径")
		}
		if extra != "" {
			return errAt(source, line, raw, "module 指令只能有一个模块路径")
		}
		if m.Module != "" {
			return errAt(source, line, raw, fmt.Sprintf("module 指令重复声明（已声明为 %s）", m.Module))
		}
		m.Module = path
		return nil
	case "require":
		path, ver, ok := pathAndVersionExpr(rest)
		if !ok {
			return errAt(source, line, raw, "require 需要“模块路径 + 版本写法”两个字段")
		}
		kind, err := ClassifyVersion(ver)
		if err != nil {
			return errAt(source, line, raw, err.Error())
		}
		m.Requires = append(m.Requires, Require{Path: path, Version: ver, Kind: kind, Line: line})
		return nil
	case "exclude":
		path, ver, ok := pathAndVersionExpr(rest)
		if !ok {
			return errAt(source, line, raw, "exclude 需要“模块路径 + 精确版本”两个字段")
		}
		if _, err := semver.Parse(ver); err != nil {
			return errAt(source, line, raw, fmt.Sprintf("exclude 只允许精确版本：%v", err))
		}
		m.Excludes = append(m.Excludes, Exclude{Path: path, Version: ver, Line: line})
		return nil
	case "replace":
		return m.parseReplace(rest, line, raw, source)
	default:
		return errAt(source, line, raw, fmt.Sprintf("未知指令 %q", dir))
	}
}

func (m *Manifest) parseReplace(rest string, line int, raw, source string) error {
	parts := strings.SplitN(rest, "=>", 2)
	if len(parts) != 2 {
		return errAt(source, line, raw, "replace 缺少 => 分隔符")
	}
	left := strings.Fields(parts[0])
	right := strings.Fields(parts[1])
	if len(left) < 1 || len(left) > 2 {
		return errAt(source, line, raw, "replace 左侧应为“模块路径 [版本]”")
	}
	if len(right) == 0 {
		return errAt(source, line, raw, "replace 右侧缺少目标（模块路径+版本或本地目录）")
	}
	rp := Replace{OldPath: unquote(left[0]), Line: line}
	if len(left) == 2 {
		rp.OldVersion = left[1]
		if _, err := semver.Parse(rp.OldVersion); err != nil {
			return errAt(source, line, raw, fmt.Sprintf("replace 左侧版本必须是精确版本：%v", err))
		}
	}
	if len(right) == 1 && strings.HasPrefix(right[0], "./") || len(right) == 1 && strings.HasPrefix(right[0], "../") {
		rp.LocalPath = unquote(right[0])
		m.Replaces = append(m.Replaces, rp)
		return nil
	}
	if len(right) != 2 {
		return errAt(source, line, raw, "replace 右侧应为“模块路径 精确版本”或“本地目录（./ 或 ../ 开头）”")
	}
	rp.NewPath = unquote(right[0])
	rp.NewVersion = right[1]
	if _, err := semver.Parse(rp.NewVersion); err != nil {
		return errAt(source, line, raw, fmt.Sprintf("replace 右侧版本必须是精确版本：%v", err))
	}
	m.Replaces = append(m.Replaces, rp)
	return nil
}

// ClassifyVersion 将原始版本写法归一为类别，并在解析阶段做语法校验，
// 让非法区间、非法伪版本等问题在这里就带原因暴露出来。
func ClassifyVersion(ver string) (VersionKind, error) {
	if ver == "latest" {
		return KindLatest, nil
	}
	if strings.HasPrefix(ver, "@") {
		name := ver[1:]
		if name == "" {
			return "", fmt.Errorf("分支引用 %q 缺少分支名", ver)
		}
		if strings.ContainsAny(name, " \t@") {
			return "", fmt.Errorf("分支名 %q 非法", name)
		}
		return KindBranch, nil
	}
	if _, err := semver.Parse(ver); err == nil {
		return KindExact, nil
	}
	if _, err := semver.ParseConstraint(ver); err == nil {
		return KindRange, nil
	}
	return "", fmt.Errorf("版本写法 %q 既不是精确版本、合法区间，也不是分支引用或 latest", ver)
}

func stripComment(s string) string {
	// 支持引号，避免 "//" 出现在路径中时被误删。
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'':
			inQuote = !inQuote
		}
		if !inQuote && i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
			return s[:i]
		}
	}
	return s
}

func splitDirective(s string) (dir, rest string, ok bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", "", false
	}
	dir = fields[0]
	idx := strings.Index(s, dir)
	rest = strings.TrimSpace(s[idx+len(dir):])
	return dir, rest, true
}

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

func oneToken(s string) (string, string, error) {
	f := strings.Fields(s)
	if len(f) < 1 {
		return "", "", fmt.Errorf("缺少字段")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, f[0]))
	return unquote(f[0]), rest, nil
}

// pathAndVersionExpr 取第一个字段为模块路径，其余字段整体作为版本表达式
// （允许区间内部带空格，如 >=1.0.0 <2.0.0；带引号时去掉一层引号）。
func pathAndVersionExpr(s string) (string, string, bool) {
	f := strings.Fields(s)
	if len(f) < 2 {
		return "", "", false
	}
	path := unquote(f[0])
	rest := strings.TrimSpace(strings.TrimPrefix(s, f[0]))
	if len(rest) >= 2 && (rest[0] == '"' || rest[0] == '\'') && rest[len(rest)-1] == rest[0] {
		rest = rest[1 : len(rest)-1]
	}
	if rest == "" {
		return "", "", false
	}
	return path, rest, true
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
