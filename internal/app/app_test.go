package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepare 在临时目录中搭建清单 + 注册数据，返回选项。
func prepare(t *testing.T, manifestSrc, regA string) Options {
	t.Helper()
	dir := t.TempDir()
	mp := filepath.Join(dir, "modules.txt")
	if err := os.WriteFile(mp, []byte(manifestSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := filepath.Join(dir, "registry", "example.com")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "a.json"), []byte(regA), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{ManifestPath: mp, RegistryDir: filepath.Join(dir, "registry"), LockPath: filepath.Join(dir, "modules-lock.json")}
}

const regA1 = `{"path":"example.com/a","versions":[{"version":"v1.0.0","digest":"sha256:aaa","requires":[]}]}`

func TestResolveThenVerifyIsStable(t *testing.T) {
	// 业务：首次 resolve 生成锁；输入不变再次 resolve 不改动锁文件；verify 通过。
	opts := prepare(t, "module app\nrequire example.com/a v1.0.0\n", regA1)
	var out bytes.Buffer
	if _, err := Resolve(opts, &out); err != nil {
		t.Fatalf("首次 resolve 失败: %v\n%s", err, out.String())
	}
	before, err := os.ReadFile(opts.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	statBefore, err := os.Stat(opts.LockPath)
	if err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if _, err := Resolve(opts, &out); err != nil {
		t.Fatalf("二次 resolve 失败: %v", err)
	}
	if !strings.Contains(out.String(), "保持不变") {
		t.Errorf("应声明锁文件保持不变，输出: %s", out.String())
	}
	after, _ := os.ReadFile(opts.LockPath)
	if !bytes.Equal(before, after) {
		t.Fatal("二次 resolve 改动了锁文件内容")
	}
	statAfter, _ := os.Stat(opts.LockPath)
	if statBefore.ModTime() != statAfter.ModTime() || statBefore.Size() != statAfter.Size() {
		t.Fatal("二次 resolve 改动了锁文件元数据（mtime/size）")
	}

	out.Reset()
	if err := Verify(opts, &out); err != nil {
		t.Fatalf("verify 应通过: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "锁定校验通过") {
		t.Errorf("verify 输出不正确: %s", out.String())
	}
}

func TestResolveMalformedManifestFailsWithLineReasons(t *testing.T) {
	// 业务：畸形清单导致 resolve 失败，错误里能看到行号和原因，且不生成锁文件。
	src := "module app\nrequire example.com/a\nrequire example.com/b >=x.y.z\n"
	opts := prepare(t, src, regA1)
	var out bytes.Buffer
	_, err := Resolve(opts, &out)
	if err == nil {
		t.Fatal("畸形清单应导致 resolve 失败")
	}
	msg := err.Error()
	for _, want := range []string{"modules.txt:2", "modules.txt:3", "声明级错误"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误中缺少 %q：%s", want, msg)
		}
	}
	if _, statErr := os.Stat(opts.LockPath); !os.IsNotExist(statErr) {
		t.Fatal("解析失败时不应生成锁文件")
	}
}

func TestVerifyDetectsManuallyEditedLock(t *testing.T) {
	// 业务：锁文件被手改（digest 被篡改）verify 报分类漂移且不自动修复。
	opts := prepare(t, "module app\nrequire example.com/a v1.0.0\n", regA1)
	var out bytes.Buffer
	if _, err := Resolve(opts, &out); err != nil {
		t.Fatal(err)
	}
	lockBytes, _ := os.ReadFile(opts.LockPath)
	tampered := bytes.Replace(lockBytes, []byte("sha256:aaa"), []byte("sha256:zzz"), 1)
	if err := os.WriteFile(opts.LockPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	err := Verify(opts, &out)
	if err == nil {
		t.Fatal("篡改后的锁应导致 verify 失败")
	}
	if !strings.Contains(out.String(), "module-digest-mismatch") {
		t.Errorf("应报摘要漂移分类: %s", out.String())
	}
	again, _ := os.ReadFile(opts.LockPath)
	if !bytes.Equal(tampered, again) {
		t.Fatal("verify 不得自动修复被篡改的锁文件")
	}
}

func TestResolveConflictDoesNotWriteLock(t *testing.T) {
	// 业务：区间冲突时 resolve 失败、输出冲突原因、不生成锁文件。
	dir := t.TempDir()
	mp := filepath.Join(dir, "modules.txt")
	manifestSrc := "module app\nrequire example.com/a v1.0.0\nrequire example.com/c <2.0.0\n"
	if err := os.WriteFile(mp, []byte(manifestSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := filepath.Join(dir, "registry", "example.com")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	aJSON := `{"path":"example.com/a","versions":[{"version":"v1.0.0","digest":"x","requires":[{"path":"example.com/c","version":">=2.0.0"}]}]}`
	cJSON := `{"path":"example.com/c","versions":[{"version":"v1.9.0","digest":"x"},{"version":"v2.0.0","digest":"y"}]}`
	if err := os.WriteFile(filepath.Join(rd, "a.json"), []byte(aJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "c.json"), []byte(cJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{ManifestPath: mp, RegistryDir: filepath.Join(dir, "registry"), LockPath: filepath.Join(dir, "modules-lock.json")}
	var out bytes.Buffer
	_, err := Resolve(opts, &out)
	if err == nil {
		t.Fatal("存在区间冲突时 resolve 应失败")
	}
	if !strings.Contains(err.Error(), "冲突") || !strings.Contains(out.String(), "conflict/range") {
		t.Errorf("应输出区间冲突分类: %v\n%s", err, out.String())
	}
	if _, statErr := os.Stat(opts.LockPath); !os.IsNotExist(statErr) {
		t.Fatal("冲突时不应生成锁文件")
	}
}
