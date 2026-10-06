package lock

import (
	"strings"
	"testing"

	"github.com/lechandonga/module-dependency-resolver-go/internal/manifest"
	"github.com/lechandonga/module-dependency-resolver-go/internal/registry"
	"github.com/lechandonga/module-dependency-resolver-go/internal/resolver"
)

func buildWorld(t *testing.T, manifestSrc, regJSON string) (*resolver.Result, string, string) {
	t.Helper()
	dir := t.TempDir()
	manifestPath := dir + "/modules.txt"
	if err := writeFile(manifestPath, manifestSrc); err != nil {
		t.Fatal(err)
	}
	regDir := dir + "/registry/example.com"
	mustMkdir(t, regDir)
	if err := writeFile(regDir+"/a.json", regJSON); err != nil {
		t.Fatal(err)
	}
	m, errs := manifest.Parse(strings.NewReader(manifestSrc), manifestPath)
	if len(errs) > 0 {
		t.Fatalf("前置清单解析失败: %v", errs)
	}
	reg, err := registry.Load(dir + "/registry")
	if err != nil {
		t.Fatal(err)
	}
	res := resolver.New(m, reg, dir).Resolve()
	return res, manifestPath, dir
}

const simpleManifest = "module app\nrequire example.com/a v1.0.0\n"
const simpleReg = `{"path":"example.com/a","versions":[{"version":"v1.0.0","digest":"sha256:aaa","requires":[]}]}`

func freshLock(t *testing.T) (*File, *resolver.Result, VerifyOptions) {
	t.Helper()
	res, manifestPath, dir := buildWorld(t, simpleManifest, simpleReg)
	mh, err := HashFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rh, err := HashRegistryTree(dir + "/registry")
	if err != nil {
		t.Fatal(err)
	}
	lf := Build(res, mh, rh, nil)
	return lf, res, VerifyOptions{ManifestHash: mh, RegistryHash: rh}
}

func TestVerifyClean(t *testing.T) {
	// 业务：输入与解析结果一致时校验通过。
	lf, res, opts := freshLock(t)
	if drifts := Verify(lf, res, opts); len(drifts) != 0 {
		t.Fatalf("不应有漂移: %v", drifts)
	}
}

func TestDriftManifestChanged(t *testing.T) {
	// 业务：清单被手改（摘要变化）要单独分类报错。
	lf, res, opts := freshLock(t)
	opts.ManifestHash = "sha256:changed"
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftInputManifest, "") {
		t.Fatalf("应报清单漂移: %v", drifts)
	}
}

func TestDriftRegistryChanged(t *testing.T) {
	// 业务：注册数据变化（比如 digest 变了）要单独分类。
	lf, res, opts := freshLock(t)
	opts.RegistryHash = "sha256:changed"
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftInputRegistry, "") {
		t.Fatalf("应报注册数据漂移: %v", drifts)
	}
}

func TestDriftLockedVersionMismatch(t *testing.T) {
	// 业务：锁版本与重新解析结果不一致要报版本漂移，不自动修。
	lf, res, opts := freshLock(t)
	lf.Modules[0].Version = "v9.9.9"
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftModuleVersion, "example.com/a") {
		t.Fatalf("应报版本漂移: %v", drifts)
	}
}

func TestDriftDigestMismatch(t *testing.T) {
	// 业务：锁摘要与当前内容摘要不一致要单独报摘要错误。
	lf, res, opts := freshLock(t)
	lf.Modules[0].Digest = "sha256:tampered"
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftModuleDigest, "example.com/a") {
		t.Fatalf("应报摘要漂移: %v", drifts)
	}
}

func TestDriftExtraLockedModule(t *testing.T) {
	// 业务：锁里有清单已经不引用的模块要报多余模块。
	lf, res, opts := freshLock(t)
	lf.Modules = append(lf.Modules, Module{Path: "example.com/ghost", Version: "v1.0.0", Digest: "sha256:g"})
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftModuleExtra, "example.com/ghost") {
		t.Fatalf("应报多余锁定模块: %v", drifts)
	}
}

func TestDriftMissingLockedModule(t *testing.T) {
	// 业务：重新解析出的模块锁里没有，要报缺失模块。
	lf, res, opts := freshLock(t)
	res.Selections["example.com/newly"] = &resolver.Selection{Path: "example.com/newly", Version: "v1.0.0", Digest: "sha256:n"}
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftModuleMissing, "example.com/newly") {
		t.Fatalf("应报缺失锁定模块: %v", drifts)
	}
}

func TestDriftLocalDirectoryChanged(t *testing.T) {
	// 业务：本地 replace 目录内容变化要报本地输入漂移。
	lf, res, _ := freshLock(t)
	res.Selections["example.com/local"] = &resolver.Selection{Path: "example.com/local", Version: "v0.0.0-local", Digest: "sha256:dir2", Local: true, LocalPath: "./local"}
	opts := VerifyOptions{ManifestHash: lf.Inputs.Manifest, RegistryHash: lf.Inputs.Registry, LocalHashes: map[string]string{"./local": "sha256:dir2"}}
	lf.Inputs.Locals = map[string]string{"./local": "sha256:dir1"}
	lf.Modules = append(lf.Modules, Module{Path: "example.com/local", Version: "v0.0.0-local", Digest: "sha256:dir2", LocalPath: "./local"})
	drifts := Verify(lf, res, opts)
	if !hasDrift(drifts, DriftInputLocal, "./local") {
		t.Fatalf("应报本地目录漂移: %v", drifts)
	}
}

func TestLockIsDeterministicAndStable(t *testing.T) {
	// 业务：同输入两次构建锁文件字节级一致；写盘幂等（不改动已有文件）。
	lf1, res1, opts := freshLock(t)
	lf2 := Build(res1, opts.ManifestHash, opts.RegistryHash, nil)
	b1, err := lf1.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := lf2.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("锁文件不确定:\n%s\n!=\n%s", b1, b2)
	}
}

func TestParseRejectsBadLock(t *testing.T) {
	// 业务：锁文件损坏/缺字段/重复模块要报解析错误，不能静默接受。
	for _, src := range []string{
		`{"lock_version":9,"root":"a","modules":[]}`,
		`{"lock_version":1,"root":"a","modules":[{"path":"","version":"v1","digest":"x"}]}`,
		`{"lock_version":1,"root":"a","modules":[{"path":"x","version":"v1","digest":"d"},{"path":"x","version":"v2","digest":"d"}]}`,
	} {
		if _, err := Parse(strings.NewReader(src)); err == nil {
			t.Errorf("锁文件应解析失败: %s", src)
		}
	}
}

func hasDrift(drifts []Drift, kind DriftKind, module string) bool {
	for _, d := range drifts {
		if d.Kind == kind && (module == "" || d.Module == module) {
			return true
		}
	}
	return false
}

func writeFile(path, content string) error {
	if err := mkdirAll(path); err != nil {
		return err
	}
	return osWriteFile(path, []byte(content))
}
