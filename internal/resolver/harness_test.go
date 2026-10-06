package resolver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lechandonga/module-dependency-resolver-go/internal/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
)

type fakeWorld struct {
	t    *testing.T
	dir  string
	reg  *registry.Registry
	mods map[string]*registry.Module
}

func newWorld(t *testing.T) *fakeWorld {
	t.Helper()
	return &fakeWorld{t: t, dir: t.TempDir(), mods: map[string]*registry.Module{}}
}

func (w *fakeWorld) mod(path string, versions ...registry.Release) *fakeWorld {
	w.mods[path] = &registry.Module{Path: path, Versions: versions, Branches: map[string]string{}}
	return w
}

func (w *fakeWorld) branch(path, branch, version string) *fakeWorld {
	w.mods[path].Branches[branch] = version
	return w
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (w *fakeWorld) build() *registry.Registry {
	regDir := filepath.Join(w.dir, "registry")
	for path, m := range w.mods {
		d := filepath.Join(regDir, filepath.Dir(path))
		if err := os.MkdirAll(d, 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(regDir, path+".json"), mustJSON(w.t, m), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	r, err := registry.Load(regDir)
	if err != nil {
		w.t.Fatal(err)
	}
	w.reg = r
	return r
}

func (w *fakeWorld) localModule(name, content string) string {
	d := filepath.Join(w.dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "modules.txt"), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return "./" + name
}

func (w *fakeWorld) resolve(src string) *Result {
	m, errs := manifest.Parse(strings.NewReader(src), "modules.txt")
	if len(errs) > 0 {
		w.t.Fatalf("前置清单解析失败: %v", errs)
	}
	if w.reg == nil {
		w.build()
	}
	return New(m, w.reg, w.dir).Resolve()
}

func hasIssue(res *Result, kind, moduleContains string) bool {
	for _, is := range res.Issues {
		if is.Kind == kind && (moduleContains == "" || strings.Contains(is.Module, moduleContains)) {
			return true
		}
	}
	return false
}

func selVersion(res *Result, path string) string {
	if s, ok := res.Selections[path]; ok {
		return s.Version
	}
	return "<未选中>"
}

func strReader(s string) *strings.Reader { return strings.NewReader(s) }
