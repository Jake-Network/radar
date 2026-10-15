// Package buildidentity identifies the running Go build without hashing its
// executable. Default Go linker build IDs bind the compiled extractor as well as
// dependencies, including unversioned development builds.
package buildidentity

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"
)

var identity struct {
	sync.Once
	value string
}

// Identity is stable for this process and, with an available Go linker build ID,
// for identical builds. Missing/unsupported build IDs use a process-local nonce:
// cross-process cache reuse is disabled rather than trusting a version label.
// Binaries intentionally linked with a forged constant -buildid are outside this
// local cache identity model; this is invalidation, not executable authentication.
func Identity() string {
	identity.Do(func() {
		name, err := os.Executable()
		if err == nil {
			f, e := os.Open(name)
			if e == nil {
				defer f.Close()
				if id, e := readID(f); e == nil && id != "" {
					sum := sha256.Sum256([]byte(id))
					identity.value = "go-build:" + hex.EncodeToString(sum[:])
					return
				}
			}
		}
		nonce := make([]byte, 32)
		if _, e := rand.Read(nonce); e != nil {
			nonce = []byte(fmt.Sprintf("%d/%d", os.Getpid(), time.Now().UnixNano()))
		}
		sum := sha256.Sum256(nonce)
		identity.value = "process:" + hex.EncodeToString(sum[:])
	})
	return identity.value
}

// Limit all metadata reads, including object-format section tables. Typical
// ELF IDs take <40 KiB, and Mach-O/PE IDs use at most 32 KiB of text.
type boundedReader struct {
	io.ReaderAt
	remaining int
}

func (r *boundedReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > r.remaining {
		return 0, fmt.Errorf("build identity metadata budget exceeded")
	}
	r.remaining -= len(p)
	return r.ReaderAt.ReadAt(p, off)
}
func readID(source io.ReaderAt) (string, error) {
	r := &boundedReader{ReaderAt: source, remaining: 256 << 10}
	head := make([]byte, 32<<10)
	n, err := r.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	head = head[:n]
	if bytes.HasPrefix(head, []byte("\x7fELF")) {
		binary, err := elf.NewFile(r)
		if err != nil {
			return "", err
		}
		section := binary.Section(".note.go.buildid")
		if section == nil || section.Size > 4096 {
			return "", fmt.Errorf("Go build note unavailable")
		}
		note, err := section.Data()
		if err != nil {
			return "", err
		}
		if len(note) < 16 {
			return "", fmt.Errorf("invalid Go build note")
		}
		names := binary.ByteOrder.Uint32(note[:4])
		size := binary.ByteOrder.Uint32(note[4:8])
		kind := binary.ByteOrder.Uint32(note[8:12])
		offset := 12 + (uint64(names)+3)&^uint64(3)
		if names != 4 || kind != 4 || !bytes.Equal(note[12:16], []byte{'G', 'o', 0, 0}) || offset+uint64(size) > uint64(len(note)) {
			return "", fmt.Errorf("invalid Go build note")
		}
		return string(note[offset : offset+uint64(size)]), nil
	}
	if id := rawID(head); id != "" {
		return id, nil
	}
	// A Mach-O header may precede its text by more than the initial read.
	if len(head) >= 4 && (bytes.Equal(head[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(head[:4], []byte{0xfe, 0xed, 0xfa, 0xcf}) || bytes.Equal(head[:4], []byte{0xce, 0xfa, 0xed, 0xfe}) || bytes.Equal(head[:4], []byte{0xfe, 0xed, 0xfa, 0xce})) {
		binary, err := macho.NewFile(r)
		if err != nil {
			return "", err
		}
		section := binary.Section("__text")
		if section == nil {
			return "", fmt.Errorf("Mach-O text unavailable")
		}
		length := min(section.Size, 32<<10)
		data := make([]byte, length)
		n, e := r.ReadAt(data, int64(section.Offset))
		if e != nil && e != io.EOF {
			return "", e
		}
		return rawID(data[:n]), nil
	}
	return "", fmt.Errorf("Go build ID unavailable")
}
func rawID(data []byte) string {
	prefix, end := []byte("\xff Go build ID: \""), []byte("\"\n \xff")
	i := bytes.Index(data, prefix)
	if i < 0 {
		return ""
	}
	rest := data[i+len(prefix):]
	j := bytes.Index(rest, end)
	if j < 0 || j > 2048 {
		return ""
	}
	id, err := strconv.Unquote("\"" + string(rest[:j]) + "\"")
	if err != nil {
		return ""
	}
	return id
}
