// Command depres resolves module dependencies from a local manifest against
// a local registry, and generates or verifies lock files.
//
//	depres resolve -manifest m.json -registry r.json -lock dep.lock
//	depres verify  -manifest m.json -registry r.json -lock dep.lock
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lechandonga/module-dependency-resolver-go/lock"
	"github.com/lechandonga/module-dependency-resolver-go/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/registry"
	"github.com/lechandonga/module-dependency-resolver-go/resolver"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	manifestPath := fs.String("manifest", "dep.json", "dependency manifest file")
	registryPath := fs.String("registry", "registry.json", "local registry file")
	lockPath := fs.String("lock", "dep.lock", "lock file")
	_ = fs.Parse(os.Args[2:])

	manifestData, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatal(err)
	}
	m, err := manifest.Parse(manifestData)
	if err != nil {
		fatal(err)
	}
	reg, err := registry.Load(*registryPath)
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "resolve":
		f, err := lock.Generate(manifestData, m, reg)
		if err != nil {
			fatal(err)
		}
		if err := f.WriteFile(*lockPath); err != nil {
			fatal(err)
		}
		for _, e := range f.Entries {
			fmt.Printf("%s %s %s\n", e.Path, e.Version, e.Digest)
		}
		fmt.Printf("locked %d module(s) to %s\n", len(f.Entries), *lockPath)
	case "verify":
		f, err := lock.ReadFile(*lockPath)
		if err != nil {
			fatal(err)
		}
		if err := lock.Verify(f, manifestData, m, reg); err != nil {
			fatal(err)
		}
		fmt.Println("lock file is up to date")
	case "graph":
		res := resolver.Resolve(m, reg)
		for _, e := range res.Entries() {
			fmt.Printf("%s %s\n", e.Path, e.Version)
		}
		for _, c := range res.Conflicts {
			fmt.Println("conflict:", c.Error())
		}
		if !res.Ok() {
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: depres <resolve|verify|graph> -manifest m.json -registry r.json -lock dep.lock")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
