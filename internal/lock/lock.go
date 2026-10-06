// Package lock 负责锁文件的生成、读取与漂移校验。
//
// 锁文件是确定性 JSON：模块按路径排序，记录每个模块的选中版本、
// 内容摘要、replace 信息以及约束来源；并记录清单与注册数据的输入摘要，
// 使“输入未变”可被直接验证。
package lock

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/lechandonga/module-dependency-resolver-go/internal/dirhash"
	"github.com/lechandonga/module-dependency-resolver-go/internal/resolver"
)

// Version 是锁文件格式版本。
const Version = 1

// Contributor 对应 resolver.Contribution。
type Contributor struct {
	Source string `json:"source"`
	Raw    string `json:"raw"`
}

// Module 是锁文件中的单个模块条目。
type Module struct {
	Path         string        `json:"path"`
	Version      string        `json:"version"`
	Digest       string        `json:"digest"`
	ReplacedFrom string        `json:"replaced_from,omitempty"`
	LocalPath    string        `json:"local_path,omitempty"`
	Contributors []Contributor `json:"contributors"`
}

// File 是锁文件的完整结构。
type File struct {
	LockVersion int         `json:"lock_version"`
	Root        string      `json:"root"`
	Inputs      InputHashes `json:"inputs"`
	Modules     []Module    `json:"modules"`
}

// InputHashes 记录影响解析结果的全部输入摘要。
type InputHashes struct {
	Manifest string            `json:"manifest"`
	Registry string            `json:"registry"`
	Locals   map[string]string `json:"locals,omitempty"`
}

// HashFile 计算单个文件的 sha256 摘要。
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// HashRegistryTree 按相对路径排序后哈希整个注册数据目录。
func HashRegistryTree(root string) (string, error) {
	type entry struct{ rel, abs string }
	var files []entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, entry{rel: filepath.ToSlash(rel), abs: path})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return "", err
		}
		fh := sha256.Sum256(data)
		fmt.Fprintf(h, "%s sha256:%s\n", f.rel, hex.EncodeToString(fh[:]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Build 根据解析结果与输入摘要构建确定性锁文件。
func Build(res *resolver.Result, manifestHash, registryHash string, localHashes map[string]string) *File {
	lf := &File{
		LockVersion: Version,
		Root:        res.Root,
		Inputs: InputHashes{
			Manifest: manifestHash,
			Registry: registryHash,
			Locals:   localHashes,
		},
	}
	paths := make([]string, 0, len(res.Selections))
	for p := range res.Selections {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sel := res.Selections[p]
		m := Module{
			Path:         sel.Path,
			Version:      sel.Version,
			Digest:       sel.Digest,
			ReplacedFrom: sel.ReplacedFrom,
			LocalPath:    sel.LocalPath,
		}
		if sel.Local {
			// 本地替换的摘要来自目录；显式标记。
		}
		for _, c := range sel.Contributors {
			m.Contributors = append(m.Contributors, Contributor{Source: c.Source, Raw: c.Raw})
		}
		lf.Modules = append(lf.Modules, m)
	}
	return lf
}

// Marshal 输出带缩进、末尾换行的确定性 JSON。
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Parse 读取锁文件。
func Parse(r io.Reader) (*File, error) {
	var f File
	dec := json.NewDecoder(r)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("锁文件不是合法 JSON：%w", err)
	}
	if f.LockVersion != Version {
		return nil, fmt.Errorf("锁文件格式版本 %d 不受支持（当前支持 %d）", f.LockVersion, Version)
	}
	if len(f.Modules) == 0 {
		return nil, fmt.Errorf("锁文件 modules 为空")
	}
	seen := map[string]bool{}
	for _, m := range f.Modules {
		if m.Path == "" || m.Version == "" || m.Digest == "" {
			return nil, fmt.Errorf("锁文件存在缺少 path/version/digest 的模块条目：%+v", m)
		}
		if seen[m.Path] {
			return nil, fmt.Errorf("锁文件中模块 %s 重复", m.Path)
		}
		seen[m.Path] = true
	}
	return &f, nil
}

// ReadFile 从磁盘读取锁文件。
func ReadFile(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// WriteFileIfChanged 仅当字节内容发生变化时写入，返回是否发生了写入。
// 这样输入没变时再次解析不会改动锁文件（mtime 也不变）。
func WriteFileIfChanged(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == string(data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// LocalHashesForResult 收集解析结果中所有本地 replace 目录的摘要。
func LocalHashesForResult(rootDir string, res *resolver.Result) (map[string]string, error) {
	out := map[string]string{}
	var paths []string
	for _, sel := range res.Selections {
		if sel.Local {
			paths = append(paths, sel.LocalPath)
		}
	}
	sort.Strings(paths)
	for _, rel := range paths {
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootDir, rel)
		}
		h, err := dirhash.HashDir(abs)
		if err != nil {
			return nil, err
		}
		out[rel] = h
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
