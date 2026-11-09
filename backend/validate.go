package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
)

func safeName(name string) bool {
	if len(name) == 0 || len(name) > 180 || strings.ContainsAny(name, "/\\:") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	ext := strings.ToLower(path.Ext(name))
	return ext == ".ysm" || ext == ".zip"
}

func validateUpload(f *os.File, name string, size int64) error {
	if !safeName(name) {
		return fmt.Errorf("请选择文件名正常的 .ysm 或 .zip 文件")
	}
	if size < 16 {
		return fmt.Errorf("文件过小，无法作为模型包")
	}
	if strings.EqualFold(path.Ext(name), ".ysm") {
		return nil
	} // YSM's encrypted format is validated by YSM itself.
	z, e := zip.NewReader(f, size)
	if e != nil {
		return fmt.Errorf("ZIP 文件无法读取")
	}
	if len(z.File) == 0 || len(z.File) > 4096 {
		return fmt.Errorf("ZIP 必须包含 1–4096 个条目")
	}
	var total uint64
	seen := map[string]bool{}
	for _, entry := range z.File {
		n := entry.Name
		clean := path.Clean(n)
		if strings.ContainsAny(n, "\\:") || strings.HasPrefix(n, "/") || clean == ".." || strings.HasPrefix(clean, "../") || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ZIP 包含不安全路径或符号链接")
		}
		for _, part := range strings.Split(n, "/") {
			if part == ".." {
				return fmt.Errorf("ZIP 包含上级目录")
			}
		}
		if seen[clean] {
			return fmt.Errorf("ZIP 包含重复路径")
		}
		seen[clean] = true
		if entry.UncompressedSize64 > 256<<20 || total > 256<<20-entry.UncompressedSize64 {
			return fmt.Errorf("ZIP 解压后超过 256 MiB")
		}
		total += entry.UncompressedSize64
		if entry.FileInfo().IsDir() {
			continue
		}
		src, e := entry.Open()
		if e != nil {
			return fmt.Errorf("无法读取 ZIP 条目")
		}
		nread, e := io.Copy(io.Discard, io.LimitReader(src, int64(entry.UncompressedSize64)+1))
		src.Close()
		if e != nil || uint64(nread) != entry.UncompressedSize64 {
			return fmt.Errorf("ZIP 校验失败")
		}
	}
	return nil
}
