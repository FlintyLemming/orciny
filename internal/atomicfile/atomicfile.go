// Package atomicfile 提供「临时文件 + rename」的原子落盘（spec §7.1）。
// agent 的所有写盘都走这里，避免写到一半崩溃留下半个文件。
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write 原子地把 data 写到 path，并设置权限位。父目录不存在时自动创建（0700）。
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录 %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这里是 no-op

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("设置权限: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入: %w", err)
	}
	// 先 fsync 再 rename：崩溃时要么是旧内容，要么是完整新内容。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("重命名到 %s: %w", path, err)
	}
	return nil
}
