// Package registry 从本地目录读取模块版本元数据，模拟远程模块仓库。
//
// 目录布局：
//
//	registry/
//	  example.com/foo/bar.json
//
// 其中 JSON 文件内容：
//
//	{
//	  "path": "example.com/foo/bar",
//	  "versions": [
//	    {"version": "v1.0.0", "digest": "sha256:...", "requires": [{"path": "...", "version": "..."}]}
//	  ],
//	  "branches": {"main": "v1.3.2-pre.1"}
//	}
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/semver"
)

// RequireRef 是某个已发布版本声明的依赖，version 支持与清单相同的写法。
type RequireRef struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Release 是一个已发布版本及其内容摘要与依赖。
type Release struct {
	Version  string       `json:"version"`
	Digest   string       `json:"digest"`
	Requires []RequireRef `json:"requires"`
}

// Module 是注册中心中一个模块路径的全部元数据。
type Module struct {
	Path     string            `json:"path"`
	Versions []Release         `json:"versions"`
	Branches map[string]string `json:"branches"`
}

// Registry 提供对本地模块元数据的只读查询。
type Registry struct {
	root    string
	modules map[string]*Module
}

// Load 读取 root 下所有 *.json 元数据。
func Load(root string) (*Registry, error) {
	r := &Registry{root: root, modules: map[string]*Module{}}
	if root == "" {
		return nil, fmt.Errorf("registry 目录为空")
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 %s 失败：%w", path, err)
		}
		var mod Module
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&mod); err != nil {
			return fmt.Errorf("解析注册数据 %s 失败：%w", path, err)
		}
		if mod.Path == "" {
			return fmt.Errorf("注册数据 %s 缺少 path 字段", path)
		}
		if _, dup := r.modules[mod.Path]; dup {
			return fmt.Errorf("模块 %s 在注册数据中重复出现（%s）", mod.Path, path)
		}
		if err := mod.validate(); err != nil {
			return fmt.Errorf("注册数据 %s 中模块 %s 非法：%w", path, mod.Path, err)
		}
		r.modules[mod.Path] = &mod
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (m *Module) validate() error {
	seen := map[string]bool{}
	for i := range m.Versions {
		rel := &m.Versions[i]
		if rel.Version == "" {
			return fmt.Errorf("存在缺少 version 字段的版本条目")
		}
		if _, err := semver.Parse(rel.Version); err != nil {
			return fmt.Errorf("版本 %s 非法：%w", rel.Version, err)
		}
		if rel.Digest == "" {
			return fmt.Errorf("版本 %s 缺少 digest 字段", rel.Version)
		}
		if seen[rel.Version] {
			return fmt.Errorf("版本 %s 重复声明", rel.Version)
		}
		seen[rel.Version] = true
		for _, req := range rel.Requires {
			if req.Path == "" || req.Version == "" {
				return fmt.Errorf("版本 %s 存在缺少 path 或 version 的依赖条目", rel.Version)
			}
		}
	}
	for name, ver := range m.Branches {
		if name == "" {
			return fmt.Errorf("存在空分支名")
		}
		if _, ok := seen[ver]; !ok {
			return fmt.Errorf("分支 %s 指向不存在的版本 %s", name, ver)
		}
	}
	return nil
}

// Has 报告模块是否存在于注册中心。
func (r *Registry) Has(path string) bool {
	_, ok := r.modules[path]
	return ok
}

// Get 返回模块元数据。
func (r *Registry) Get(path string) (*Module, error) {
	m, ok := r.modules[path]
	if !ok {
		return nil, fmt.Errorf("模块 %s 在本地注册中心不存在", path)
	}
	return m, nil
}

// Release 查询某模块的指定精确版本。
func (r *Registry) Release(path, version string) (*Release, error) {
	m, err := r.Get(path)
	if err != nil {
		return nil, err
	}
	for i := range m.Versions {
		if m.Versions[i].Version == version {
			return &m.Versions[i], nil
		}
	}
	return nil, fmt.Errorf("模块 %s 没有版本 %s", path, version)
}

// ResolveBranch 把分支引用解析为它当前指向的精确版本。
func (r *Registry) ResolveBranch(path, branch string) (string, error) {
	m, err := r.Get(path)
	if err != nil {
		return "", err
	}
	ver, ok := m.Branches[branch]
	if !ok {
		return "", fmt.Errorf("模块 %s 不存在分支 %s", path, branch)
	}
	return ver, nil
}

// Latest 返回模块的 latest：优先取最高稳定版，没有稳定版时取最高预发布版。
func (r *Registry) Latest(path string) (string, error) {
	m, err := r.Get(path)
	if err != nil {
		return "", err
	}
	var stable, all []semver.Version
	for _, rel := range m.Versions {
		v, err := semver.Parse(rel.Version)
		if err != nil {
			continue
		}
		all = append(all, v)
		if !v.IsPrerelease() {
			stable = append(stable, v)
		}
	}
	pick := stable
	if len(pick) == 0 {
		pick = all
	}
	if len(pick) == 0 {
		return "", fmt.Errorf("模块 %s 没有任何可用版本", path)
	}
	sort.Slice(pick, func(i, j int) bool { return semver.Compare(pick[i], pick[j]) < 0 })
	return pick[len(pick)-1].String(), nil
}

// SortedVersions 返回按 semver 升序排列的全部版本。
func (m *Module) SortedVersions() []semver.Version {
	vs := make([]semver.Version, 0, len(m.Versions))
	for _, rel := range m.Versions {
		if v, err := semver.Parse(rel.Version); err == nil {
			vs = append(vs, v)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return semver.Compare(vs[i], vs[j]) < 0 })
	return vs
}
