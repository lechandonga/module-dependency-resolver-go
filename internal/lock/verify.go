package lock

import (
	"fmt"
	"sort"

	"github.com/lechandonga/module-dependency-resolver-go/internal/resolver"
)

// DriftKind 细分锁定漂移的原因，调用方可据此输出不同错误。
type DriftKind string

const (
	// DriftInputManifest 清单内容相对锁定时发生变化。
	DriftInputManifest DriftKind = "input-manifest-changed"
	// DriftInputRegistry 注册数据内容相对锁定时发生变化。
	DriftInputRegistry DriftKind = "input-registry-changed"
	// DriftInputLocal 本地 replace 目录内容发生变化。
	DriftInputLocal DriftKind = "input-local-changed"
	// DriftModuleVersion 锁里版本与重新解析的结果不一致。
	DriftModuleVersion DriftKind = "module-version-drift"
	// DriftModuleDigest 锁里摘要与当前实际内容摘要不一致。
	DriftModuleDigest DriftKind = "module-digest-mismatch"
	// DriftModuleExtra 锁中存在清单已不再引用的模块。
	DriftModuleExtra DriftKind = "extra-locked-module"
	// DriftModuleMissing 重新解析得到的模块在锁里不存在。
	DriftModuleMissing DriftKind = "missing-locked-module"
	// DriftRoot 根模块路径不一致。
	DriftRoot DriftKind = "root-changed"
)

// Drift 是一条具体漂移。
type Drift struct {
	Kind   DriftKind
	Module string
	Detail string
}

func (d Drift) Error() string {
	switch {
	case d.Module != "":
		return fmt.Sprintf("[%s] %s: %s", d.Kind, d.Module, d.Detail)
	default:
		return fmt.Sprintf("[%s] %s", d.Kind, d.Detail)
	}
}

// VerifyOptions 携带当前输入摘要。
type VerifyOptions struct {
	ManifestHash string
	RegistryHash string
	LocalHashes  map[string]string
}

// Verify 把锁文件与重新解析的结果及当前输入摘要逐项比对。
// 不做任何自动修复；全部漂移都会收集后返回。
func Verify(lf *File, res *resolver.Result, opts VerifyOptions) []Drift {
	var drifts []Drift

	if lf.Root != res.Root {
		drifts = append(drifts, Drift{Kind: DriftRoot, Detail: fmt.Sprintf("锁记录根模块为 %s，当前清单为 %s", lf.Root, res.Root)})
	}
	if lf.Inputs.Manifest != opts.ManifestHash {
		drifts = append(drifts, Drift{Kind: DriftInputManifest,
			Detail: fmt.Sprintf("清单摘要不匹配（锁=%s，当前=%s），清单可能被手改", lf.Inputs.Manifest, opts.ManifestHash)})
	}
	if lf.Inputs.Registry != opts.RegistryHash {
		drifts = append(drifts, Drift{Kind: DriftInputRegistry,
			Detail: fmt.Sprintf("注册数据摘要不匹配（锁=%s，当前=%s）", lf.Inputs.Registry, opts.RegistryHash)})
	}
	for path, h := range opts.LocalHashes {
		if got := lf.Inputs.Locals[path]; got != h {
			drifts = append(drifts, Drift{Kind: DriftInputLocal, Module: path,
				Detail: fmt.Sprintf("本地目录摘要不匹配（锁=%s，当前=%s）", got, h)})
		}
	}
	for path := range lf.Inputs.Locals {
		if _, ok := opts.LocalHashes[path]; !ok {
			drifts = append(drifts, Drift{Kind: DriftInputLocal, Module: path,
				Detail: fmt.Sprintf("锁记录的本地 replace 目录 %s 已不再使用", path)})
		}
	}

	locked := map[string]*Module{}
	for i := range lf.Modules {
		locked[lf.Modules[i].Path] = &lf.Modules[i]
	}

	paths := make([]string, 0, len(res.Selections))
	for p := range res.Selections {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sel := res.Selections[p]
		m, ok := locked[p]
		if !ok {
			drifts = append(drifts, Drift{Kind: DriftModuleMissing, Module: p,
				Detail: fmt.Sprintf("重新解析选中 %s，但锁文件中不存在该模块", sel.Version)})
			continue
		}
		if m.Version != sel.Version {
			drifts = append(drifts, Drift{Kind: DriftModuleVersion, Module: p,
				Detail: fmt.Sprintf("锁版本 %s 与重新解析结果 %s 不一致", m.Version, sel.Version)})
		}
		if m.Digest != sel.Digest {
			drifts = append(drifts, Drift{Kind: DriftModuleDigest, Module: p,
				Detail: fmt.Sprintf("锁摘要 %s 与当前内容摘要 %s 不一致", m.Digest, sel.Digest)})
		}
		if (m.LocalPath != "") != sel.Local || m.LocalPath != sel.LocalPath {
			drifts = append(drifts, Drift{Kind: DriftModuleVersion, Module: p,
				Detail: fmt.Sprintf("锁记录本地替换为 %q，当前为 %q", m.LocalPath, sel.LocalPath)})
		}
	}

	for _, m := range lf.Modules {
		if _, ok := res.Selections[m.Path]; !ok {
			drifts = append(drifts, Drift{Kind: DriftModuleExtra, Module: m.Path,
				Detail: fmt.Sprintf("锁文件保留 %s %s，但清单及其依赖图已不再引用该模块", m.Path, m.Version)})
		}
	}

	sort.SliceStable(drifts, func(i, j int) bool {
		if drifts[i].Kind != drifts[j].Kind {
			return drifts[i].Kind < drifts[j].Kind
		}
		return drifts[i].Module < drifts[j].Module
	})
	return drifts
}
