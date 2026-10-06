// Command mdr 是本地模块依赖解析与锁定校验工具。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lechandonga/module-dependency-resolver-go/internal/app"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	manifest := fs.String("manifest", "modules.txt", "依赖清单路径")
	registryDir := fs.String("registry", "registry", "本地注册数据目录")
	lockPath := fs.String("lock", "modules-lock.json", "锁文件路径")
	switch cmd {
	case "resolve", "verify":
		_ = fs.Parse(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	opts := app.Options{ManifestPath: *manifest, RegistryDir: *registryDir, LockPath: *lockPath}
	var err error
	switch cmd {
	case "resolve":
		_, err = app.Resolve(opts, os.Stdout)
	case "verify":
		err = app.Verify(opts, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: mdr <resolve|verify> [-manifest modules.txt] [-registry registry] [-lock modules-lock.json]")
}
