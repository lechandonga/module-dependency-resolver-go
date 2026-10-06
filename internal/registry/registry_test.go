package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeReg(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndLatestAndBranch(t *testing.T) {
	// 业务：注册数据校验、latest 规则（优先稳定版）与分支解析。
	dir := t.TempDir()
	writeReg(t, dir, "example.com/a.json", `{
	  "path": "example.com/a",
	  "versions": [
	    {"version": "v1.0.0", "digest": "sha256:1", "requires": []},
	    {"version": "v1.2.0-beta.1", "digest": "sha256:2", "requires": []}
	  ],
	  "branches": {"main": "v1.2.0-beta.1"}
	}`)
	r, err := Load(dir)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if got, _ := r.Latest("example.com/a"); got != "v1.0.0" {
		t.Errorf("有稳定版时 latest 应取 v1.0.0，实际 %s", got)
	}
	if got, err := r.ResolveBranch("example.com/a", "main"); err != nil || got != "v1.2.0-beta.1" {
		t.Errorf("分支解析=%q,%v", got, err)
	}
	if _, err := r.ResolveBranch("example.com/a", "nope"); err == nil {
		t.Error("不存在分支应报错")
	}
}

func TestLoadRejectsBadData(t *testing.T) {
	// 业务：注册数据缺字段、版本非法、分支悬空都要带原因报错。
	cases := map[string]string{
		"缺 path":     `{"versions":[{"version":"v1.0.0","digest":"x"}]}`,
		"版本缺 digest": `{"path":"p","versions":[{"version":"v1.0.0"}]}`,
		"版本非法":       `{"path":"p","versions":[{"version":"vx","digest":"x"}]}`,
		"依赖条目缺字段":    `{"path":"p","versions":[{"version":"v1.0.0","digest":"x","requires":[{"path":""}]}]}`,
		"分支指向不存在版本":  `{"path":"p","versions":[{"version":"v1.0.0","digest":"x"}],"branches":{"main":"v9.9.9"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeReg(t, dir, "p.json", content)
			if _, err := Load(dir); err == nil {
				t.Fatalf("该数据应加载失败：%s", name)
			}
		})
	}
}
