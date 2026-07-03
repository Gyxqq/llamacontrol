package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"archive/zip"

	"github.com/bodgit/sevenzip"
)

// extractArchive extracts a .7z or .zip archive to the destination directory.
func extractArchive(archivePath, destDir string) error {
	ext := strings.ToLower(filepath.Ext(archivePath))
	switch ext {
	case ".7z":
		r, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("打开 7z 文件失败: %w", err)
		}
		defer r.Close()

		for _, f := range r.File {
			fpath := filepath.Join(destDir, f.Name)

			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(fpath, 0755); err != nil {
					return err
				}
				continue
			}

			// Create parent directories
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("打开 7z 内文件 %s 失败: %w", f.Name, err)
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建文件 %s 失败: %w", fpath, err)
			}

			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("写入文件 %s 失败: %w", fpath, err)
			}
		}
		return nil

	case ".zip":
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("打开 zip 文件失败: %w", err)
		}
		defer r.Close()

		for _, f := range r.File {
			fpath := filepath.Join(destDir, f.Name)

			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(fpath, 0755); err != nil {
					return err
				}
				continue
			}

			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				return err
			}

			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("打开 zip 内文件 %s 失败: %w", f.Name, err)
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				rc.Close()
				return fmt.Errorf("创建文件 %s 失败: %w", fpath, err)
			}

			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("写入文件 %s 失败: %w", fpath, err)
			}
		}
		return nil

	default:
		return fmt.Errorf("不支持的压缩格式: %s (仅支持 .7z 和 .zip)", ext)
	}
}

// copyDirContents recursively copies all files and directories from src to dst.
// It overwrites existing files and creates dst if it doesn't exist.
func copyDirContents(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Compute path relative to src
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// Copy file contents
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, srcFile)
		return err
	})
}