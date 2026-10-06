// Package dirhash 计算本地目录的确定性内容摘要。
package dirhash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// HashDir 以相对路径（使用 / 分隔）排序后逐个哈希文件，
// 目录变化（增删文件、改动内容、改名）都会改变摘要；
// 不记录文件权限与修改时间，保证不同机器上同内容目录摘要一致。
func HashDir(root string) (string, error) {
	type fileEntry struct{ rel, abs string }
	var files []fileEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, fileEntry{rel: filepath.ToSlash(rel), abs: path})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("遍历目录 %s 失败：%w", root, err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("本地替换目录 %s 为空或不存在", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\n", f.rel)
		fh, err := os.Open(f.abs)
		if err != nil {
			return "", fmt.Errorf("打开 %s 失败：%w", f.abs, err)
		}
		fh256 := sha256.New()
		if _, err := io.Copy(fh256, fh); err != nil {
			fh.Close()
			return "", fmt.Errorf("读取 %s 失败：%w", f.abs, err)
		}
		fh.Close()
		fmt.Fprintf(h, "sha256:%s\n", hex.EncodeToString(fh256.Sum(nil)))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
