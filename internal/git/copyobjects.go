package git

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyObjectStore copies a bounded, ordinary local object store into a newly
// initialized private repository. Every file receives independent bytes: there
// are no alternates, hard links, source configuration, or references. Unusual
// stores and concurrent maintenance fall back to Git's pack-stream transport.
// The destination must be a fresh repository, with no objects of its own.
func CopyObjectStore(ctx context.Context, source, destination string) (bool, error) {
	objectDir := func(root string) (string, error) {
		p, err := RunPrivate(ctx, root, "rev-parse", "--git-path", "objects")
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return filepath.Abs(p)
	}
	src, err := objectDir(source)
	if err != nil {
		return false, nil
	}
	dst, err := objectDir(destination)
	if err != nil {
		return false, err
	}
	dstCanonical, err := filepath.EvalSymlinks(dst)
	if err != nil || dstCanonical != dst {
		return false, fmt.Errorf("private object directory is not a canonical directory")
	}
	destinationEntries, err := os.ReadDir(dst)
	if err != nil {
		return false, err
	}
	for _, entry := range destinationEntries {
		if (entry.Name() != "pack" && entry.Name() != "info") || !entry.IsDir() {
			return false, fmt.Errorf("private object directory is not empty")
		}
		children, err := os.ReadDir(filepath.Join(dst, entry.Name()))
		if err != nil || len(children) != 0 {
			return false, fmt.Errorf("private object directory is not empty")
		}
	}
	canonical, err := filepath.EvalSymlinks(src)
	if err != nil || canonical != src || src == dst {
		return false, nil
	}
	// Alternates can provide objects absent from this directory. Git handles them
	// through the fallback; they are never copied into the private repository.
	if _, err := os.Lstat(filepath.Join(src, "info", "alternates")); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, nil
	}
	type objectFile struct {
		relative string
		size     int64
	}
	var files []objectFile
	var bytes int64
	entries, err := os.ReadDir(src)
	if err != nil {
		return false, nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			return false, nil
		}
		if name == "info" {
			continue
		}
		if name != "pack" && (len(name) != 2 || !hexName(name)) {
			return false, nil
		}
		children, err := os.ReadDir(filepath.Join(src, name))
		if err != nil {
			return false, nil
		}
		if name == "pack" {
			pairs := map[string]map[string]bool{}
			for _, child := range children {
				n := child.Name()
				ext := filepath.Ext(n)
				if ext != ".pack" && ext != ".idx" && ext != ".rev" {
					continue
				}
				stem := strings.TrimSuffix(n, ext)
				if pairs[stem] == nil {
					pairs[stem] = map[string]bool{}
				}
				pairs[stem][ext] = true
			}
			for _, pair := range pairs {
				if !pair[".pack"] || !pair[".idx"] {
					return false, nil
				}
			}
		}
		for _, child := range children {
			if child.Type()&os.ModeSymlink != 0 {
				return false, nil
			}
			n := child.Name()
			if name == "pack" {
				ext := filepath.Ext(n)
				if ext != ".pack" && ext != ".idx" && ext != ".rev" {
					continue
				}
				stem := strings.TrimSuffix(strings.TrimPrefix(n, "pack-"), ext)
				if !strings.HasPrefix(n, "pack-") || (len(stem) != 40 && len(stem) != 64) || !hexName(stem) {
					return false, nil
				}
			} else if (len(n) != 38 && len(n) != 62) || !hexName(n) {
				return false, nil
			}
			info, err := child.Info()
			if err != nil || !info.Mode().IsRegular() {
				return false, nil
			}
			bytes += info.Size()
			if bytes > 512<<20 || len(files) >= 100000 {
				return false, nil
			}
			files = append(files, objectFile{filepath.Join(name, n), info.Size()})
		}
	}
	// Empty pack/loose stores may be backed by a promisor remote. Never allow
	// automatic fetching: the existing private pack-stream path handles failure.
	if len(files) == 0 {
		return false, nil
	}
	cleanup := func() {
		_ = os.RemoveAll(dst)
		_ = os.MkdirAll(filepath.Join(dst, "pack"), 0755)
		_ = os.MkdirAll(filepath.Join(dst, "info"), 0755)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			cleanup()
			return false, err
		}
		srcPath := filepath.Join(src, file.relative)
		dstPath := filepath.Join(dst, file.relative)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			cleanup()
			return false, err
		}
		info, err := os.Lstat(srcPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() != file.size {
			cleanup()
			return false, nil
		}
		in, err := os.Open(srcPath)
		if err != nil {
			cleanup()
			return false, nil
		}
		if filepath.Dir(file.relative) == "pack" && !packFooterMatches(in, file.relative, file.size) {
			_ = in.Close()
			cleanup()
			return false, nil
		}
		opened, statErr := in.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() != file.size {
			_ = in.Close()
			cleanup()
			return false, nil
		}
		out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = in.Close()
			cleanup()
			return false, err
		}
		n, copyErr := io.Copy(out, &contextObjectReader{ctx: ctx, reader: io.LimitReader(in, file.size+1)})
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil || closeErr != nil || n != file.size {
			cleanup()
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
	}
	return true, nil
}

func hexName(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return s != ""
}

type contextObjectReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextObjectReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Both .pack and .idx trailers identify the same named pack. This catches
// incomplete or mismatched pack/index publication during concurrent repacking.
func packFooterMatches(file *os.File, relative string, size int64) bool {
	ext := filepath.Ext(relative)
	if ext != ".pack" && ext != ".idx" && ext != ".rev" {
		return true
	}
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(relative), "pack-"), ext)
	expected, err := hex.DecodeString(name)
	if err != nil {
		return false
	}
	offset := int64(len(expected))
	if ext != ".pack" {
		offset *= 2
	}
	if size < offset {
		return false
	}
	actual := make([]byte, len(expected))
	if _, err := file.ReadAt(actual, size-offset); err != nil {
		return false
	}
	return bytes.Equal(actual, expected)
}
