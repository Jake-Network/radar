// Package runproof keeps immutable, repository-bound gate reports. Digests
// detect edits; they do not authenticate the writer or prove an owner review.
package runproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Jake-Network/radar/internal/pathutil"
)

const MaxBytes = 8 << 20

var idPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Record struct {
	Version    int             `json:"version"`
	Repository string          `json:"repository"`
	Report     json.RawMessage `json:"report"`
	ID         string          `json:"id"`
}

func digest(r Record) (string, error) {
	r.ID = ""
	b, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func recordPath(state, repository, id string) (string, error) {
	if repository == "" || !idPattern.MatchString(id) {
		return "", errors.New("invalid repository or gate run ID")
	}
	h := sha256.Sum256([]byte(repository))
	return pathutil.StatePath(state, filepath.Join("gate-proofs", hex.EncodeToString(h[:]), id+".json"))
}

func Save(state, repository string, report any) (Record, error) {
	r := Record{Version: 1, Repository: repository}
	b, err := json.Marshal(report)
	if err != nil {
		return r, err
	}
	if len(b) > MaxBytes {
		return r, errors.New("gate proof exceeds size limit")
	}
	r.Report = b
	r.ID, err = digest(r)
	if err != nil {
		return r, err
	}
	p, err := recordPath(state, repository, r.ID)
	if err != nil {
		return r, err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return r, err
	}
	p, err = recordPath(state, repository, r.ID)
	if err != nil {
		return r, err
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxBytes {
		return r, errors.New("gate proof exceeds size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".proof-")
	if err != nil {
		return r, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return r, err
	}
	if err = f.Close(); err != nil {
		return r, err
	}
	// A hard-link publishes complete bytes without replacing an earlier record.
	if err = os.Link(f.Name(), p); err != nil {
		if !os.IsExist(err) {
			return r, err
		}
		if _, err = Load(state, repository, r.ID); err != nil {
			return r, err
		}
	}
	return r, nil
}

func Load(state, repository, id string) (Record, error) {
	r := Record{}
	p, err := recordPath(state, repository, id)
	if err != nil {
		return r, err
	}
	f, err := os.Open(p)
	if err != nil {
		return r, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > MaxBytes {
		return r, errors.New("invalid gate proof file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return r, err
	}
	if len(b) > MaxBytes || json.Unmarshal(b, &r) != nil {
		return r, errors.New("unreadable gate proof")
	}
	want, err := digest(r)
	if err != nil || r.Version != 1 || r.Repository != repository || r.ID != id || want != id {
		return Record{}, fmt.Errorf("gate proof identity or digest mismatch")
	}
	return r, nil
}
