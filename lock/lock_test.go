package lock

import (
	"strings"
	"testing"

	"github.com/lechandonga/module-dependency-resolver-go/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/registry"
)

var registryJSON = `{"modules":[
  {"path":"a","versions":[
    {"version":"v1.0.0","content":"a1","requires":{"b":">=1.0.0 <2.0.0"}},
    {"version":"v1.1.0","content":"a11","requires":{"b":">=1.0.0 <2.0.0"}}]},
  {"path":"b","versions":[
    {"version":"v1.0.0","content":"b1"},
    {"version":"v1.5.0","content":"b15"}]}
]}`

const manifestJSON = `{"module":"app","requires":[{"path":"a","version":"[1.0.0,2.0.0)"}]}`

func setup(t *testing.T) (*manifest.Manifest, *registry.Registry) {
	t.Helper()
	m, err := manifest.Parse([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	r, err := registry.Parse([]byte(registryJSON))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return m, r
}

func driftsOf(err error, kind DriftKind) []*Drift {
	var out []*Drift
	if ds, ok := err.(Drifts); ok {
		for _, d := range ds {
			if d.Kind == kind {
				out = append(out, d)
			}
		}
	}
	return out
}

// TestGenerateAndVerify covers business: 锁定生成 —— 解析结果写入锁文件，
// 记录版本与内容摘要，未漂移时校验通过。
func TestGenerateAndVerify(t *testing.T) {
	t.Log("业务覆盖: 锁定 — 生成锁文件并校验一致")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(f.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(f.Entries))
	}
	for _, e := range f.Entries {
		if !strings.HasPrefix(e.Digest, "sha256:") {
			t.Fatalf("entry %+v missing digest", e)
		}
	}
	if err := Verify(f, []byte(manifestJSON), m, reg); err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("锁定通过: %d 个模块写入锁文件，校验一致", len(f.Entries))
}

// TestIdempotent covers business: 输入没变时再次解析不改动锁文件。
func TestIdempotent(t *testing.T) {
	t.Log("业务覆盖: 锁定幂等 — 输入不变，重复生成字节一致")
	m, reg := setup(t)
	f1, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b1, _ := f1.Marshal()
	f2, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b2, _ := f2.Marshal()
	if string(b1) != string(b2) {
		t.Fatalf("lock not stable:\n%s\nvs\n%s", b1, b2)
	}
	t.Log("幂等通过: 两次生成的锁文件逐字节一致")
}

// TestManifestChanged covers business: 清单被手改后校验报 manifest_changed。
func TestManifestChanged(t *testing.T) {
	t.Log("业务覆盖: 锁定漂移 — 清单内容被修改")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	edited := `{"module":"app","requires":[{"path":"a","version":"v1.0.0"}]}`
	me, err := manifest.Parse([]byte(edited))
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	err = Verify(f, []byte(edited), me, reg)
	if len(driftsOf(err, DriftManifestChanged)) != 1 {
		t.Fatalf("expected manifest_changed, got %v", err)
	}
	t.Logf("清单漂移通过: %v", err)
}

// TestStaleEntry covers business: 锁里有清单已不引用的模块时报 stale_entry。
func TestStaleEntry(t *testing.T) {
	t.Log("业务覆盖: 锁定漂移 — 锁中残留不再引用的模块")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// shrink manifest to drop b (a@v1.0.0 still requires b, so pin a away instead)
	edited := `{"module":"app","requires":[]}`
	me, _ := manifest.Parse([]byte(edited))
	err = Verify(f, []byte(edited), me, reg)
	if len(driftsOf(err, DriftStaleEntry)) != 2 {
		t.Fatalf("expected 2 stale entries, got %v", err)
	}
	t.Logf("残留条目通过: %v", err)
}

// TestDigestMismatch covers business: 锁中摘要与 registry 内容不符时报
// digest_mismatch，且不自动修复。
func TestDigestMismatch(t *testing.T) {
	t.Log("业务覆盖: 锁定漂移 — 摘要不匹配")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	f.Entries[0].Digest = "sha256:tampered"
	err = Verify(f, []byte(manifestJSON), m, reg)
	ds := driftsOf(err, DriftDigestMismatch)
	if len(ds) != 1 {
		t.Fatalf("expected digest_mismatch, got %v", err)
	}
	if f.Entries[0].Digest != "sha256:tampered" {
		t.Fatalf("verify must not repair the lock")
	}
	t.Logf("摘要漂移通过: %v（锁文件未被自动修复）", ds[0])
}

// TestVersionMismatch covers business: 锁的版本与重新解析结果不一致时报
// version_mismatch。
func TestVersionMismatch(t *testing.T) {
	t.Log("业务覆盖: 锁定漂移 — 锁定版本与重新解析不一致")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for i := range f.Entries {
		if f.Entries[i].Path == "b" {
			f.Entries[i].Version = "v1.0.0" // resolution selects v1.5.0
		}
	}
	err = Verify(f, []byte(manifestJSON), m, reg)
	ds := driftsOf(err, DriftVersionMismatch)
	if len(ds) != 1 || ds[0].Path != "b" {
		t.Fatalf("expected version_mismatch on b, got %v", err)
	}
	t.Logf("版本漂移通过: %v", ds[0])
}

// TestMissingEntry covers business: 锁缺少解析选中的模块时报 missing_entry。
func TestMissingEntry(t *testing.T) {
	t.Log("业务覆盖: 锁定漂移 — 锁缺少选中模块")
	m, reg := setup(t)
	f, err := Generate([]byte(manifestJSON), m, reg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	f.Entries = f.Entries[:1]
	err = Verify(f, []byte(manifestJSON), m, reg)
	if len(driftsOf(err, DriftMissingEntry)) != 1 {
		t.Fatalf("expected missing_entry, got %v", err)
	}
	t.Logf("缺失条目通过: %v", err)
}
