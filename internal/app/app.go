// Package app 编排清单解析、依赖解析、锁文件生成与校验流程。
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lechandonga/module-dependency-resolver-go/internal/lock"
	"github.com/lechandonga/module-dependency-resolver-go/internal/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
	"github.com/lechandonga/module-dependency-resolver-go/internal/resolver"
)

// Options 是命令行参数归一后的选项。
type Options struct {
	ManifestPath string
	RegistryDir  string
	LockPath     string
}

// ResolveResult 汇总一次解析运行的结果。
type ResolveResult struct {
	Manifest *manifest.Manifest
	Result   *resolver.Result
	Lock     *lock.File
	Wrote    bool
}

func loadInputs(opts Options) (*manifest.Manifest, *registry.Registry, string, error) {
	mf, err := os.Open(opts.ManifestPath)
	if err != nil {
		return nil, nil, "", fmt.Errorf("打开清单失败：%w", err)
	}
	defer mf.Close()
	m, errs := manifest.Parse(mf, opts.ManifestPath)
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, "  - "+e.Error())
		}
		return nil, nil, "", fmt.Errorf("清单解析失败，共 %d 条声明级错误：\n%s", len(errs), strings.Join(msgs, "\n"))
	}
	reg, err := registry.Load(opts.RegistryDir)
	if err != nil {
		return nil, nil, "", err
	}
	rootDir := filepath.Dir(opts.ManifestPath)
	return m, reg, rootDir, nil
}

// Resolve 解析依赖并写入/校验锁文件。
func Resolve(opts Options, out io.Writer) (*ResolveResult, error) {
	m, reg, rootDir, err := loadInputs(opts)
	if err != nil {
		return nil, err
	}
	res := resolver.New(m, reg, rootDir).Resolve()

	localHashes, err := lock.LocalHashesForResult(rootDir, res)
	if err != nil {
		return nil, err
	}
	manifestHash, err := lock.HashFile(opts.ManifestPath)
	if err != nil {
		return nil, err
	}
	registryHash, err := lock.HashRegistryTree(opts.RegistryDir)
	if err != nil {
		return nil, err
	}
	lf := lock.Build(res, manifestHash, registryHash, localHashes)

	rr := &ResolveResult{Manifest: m, Result: res, Lock: lf}

	if len(res.Issues) > 0 {
		printIssues(out, res)
		return rr, fmt.Errorf("依赖解析发现 %d 个冲突/错误，未生成锁文件", len(res.Issues))
	}

	data, err := lf.Marshal()
	if err != nil {
		return nil, err
	}
	rr.Wrote, err = lock.WriteFileIfChanged(opts.LockPath, data)
	if err != nil {
		return nil, err
	}
	printSelection(out, res, rr.Wrote, opts.LockPath)
	return rr, nil
}

// Verify 重新解析并与现有锁文件逐项比对，任何漂移都作为错误返回，绝不修改锁文件。
func Verify(opts Options, out io.Writer) error {
	m, reg, rootDir, err := loadInputs(opts)
	if err != nil {
		return err
	}
	lf, err := lock.ReadFile(opts.LockPath)
	if err != nil {
		return fmt.Errorf("读取锁文件失败（可先运行 resolve 生成）：%w", err)
	}
	res := resolver.New(m, reg, rootDir).Resolve()

	localHashes, err := lock.LocalHashesForResult(rootDir, res)
	if err != nil {
		return err
	}
	manifestHash, err := lock.HashFile(opts.ManifestPath)
	if err != nil {
		return err
	}
	registryHash, err := lock.HashRegistryTree(opts.RegistryDir)
	if err != nil {
		return err
	}

	var blocks []string
	if len(res.Issues) > 0 {
		var msgs []string
		for _, is := range res.Issues {
			msgs = append(msgs, "  - "+is.Error())
		}
		blocks = append(blocks, fmt.Sprintf("依赖解析发现 %d 个冲突/错误：\n%s", len(res.Issues), strings.Join(msgs, "\n")))
	}
	drifts := lock.Verify(lf, res, lock.VerifyOptions{
		ManifestHash: manifestHash,
		RegistryHash: registryHash,
		LocalHashes:  localHashes,
	})
	if len(drifts) > 0 {
		var msgs []string
		for _, d := range drifts {
			msgs = append(msgs, "  - "+d.Error())
		}
		blocks = append(blocks, fmt.Sprintf("锁定漂移 %d 处（已按原因分类，不会自动修复）：\n%s", len(drifts), strings.Join(msgs, "\n")))
	}
	if len(blocks) > 0 {
		fmt.Fprintln(out, strings.Join(blocks, "\n"))
		return fmt.Errorf("锁定校验失败")
	}
	fmt.Fprintf(out, "锁定校验通过：%d 个模块，锁文件与清单及内容摘要完全一致\n", len(lf.Modules))
	return nil
}

func printIssues(out io.Writer, res *resolver.Result) {
	var conflicts, errorsN int
	for _, is := range res.Issues {
		switch is.Type {
		case resolver.IssueConflict:
			conflicts++
		case resolver.IssueError:
			errorsN++
		}
	}
	fmt.Fprintf(out, "解析未通过：冲突 %d 个，错误 %d 个\n", conflicts, errorsN)
	for _, is := range res.Issues {
		fmt.Fprintln(out, "  - "+is.Error())
	}
}

func printSelection(out io.Writer, res *resolver.Result, wrote bool, lockPath string) {
	if wrote {
		fmt.Fprintf(out, "已生成/更新锁文件 %s，锁定 %d 个模块\n", lockPath, len(res.Selections))
	} else {
		fmt.Fprintf(out, "输入未变化，锁文件 %s 保持不变，锁定 %d 个模块\n", lockPath, len(res.Selections))
	}
	paths := make([]string, 0, len(res.Selections))
	for p := range res.Selections {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sel := res.Selections[p]
		extra := ""
		if sel.Local {
			extra = " -> 本地目录 " + sel.LocalPath
		} else if sel.ReplacedFrom != "" {
			extra = " (replace " + sel.ReplacedFrom + ")"
		}
		fmt.Fprintf(out, "  %s %s%s\n", p, sel.Version, extra)
	}
}
