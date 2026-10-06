package lock

import (
	"os"
	"path/filepath"
)

func mustMkdir(t interface{ Fatalf(string, ...any) }, path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func mkdirAll(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
