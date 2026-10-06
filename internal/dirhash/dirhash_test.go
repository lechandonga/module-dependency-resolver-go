package dirhash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashDirDeterministicAndSensitive(t *testing.T) {
	// 业务：同内容目录摘要稳定且与顺序无关；内容/文件集变化必须改变摘要。
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "b.txt"), "b")
	mustWrite(t, filepath.Join(dir, "a", "c.txt"), "c")
	h1, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("相同目录摘要不稳定: %s != %s", h1, h2)
	}

	// 改内容应改变摘要。
	mustWrite(t, filepath.Join(dir, "b.txt"), "changed")
	h3, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h1 {
		t.Fatal("修改文件内容后摘要未变化")
	}

	// 增删文件应改变摘要。
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	h4, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h4 == h3 {
		t.Fatal("删除文件后摘要未变化")
	}
}

func TestHashDirEmptyOrMissing(t *testing.T) {
	// 业务：空目录/不存在目录应报错，不能得到空摘要后静默通过。
	dir := t.TempDir()
	if _, err := HashDir(dir); err == nil {
		t.Fatal("空目录应报错")
	}
	if _, err := HashDir(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("不存在目录应报错")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
