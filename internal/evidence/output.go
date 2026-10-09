package evidence

import (
	"bytes"
	"crypto/sha256"
	"hash"
	"sync"
)

const maxOutput = 1 << 20

type output struct {
	mu       sync.Mutex
	b        bytes.Buffer
	exceeded bool
	digest   hash.Hash
}

func (o *output) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.digest == nil {
		o.digest = sha256.New()
	}
	o.digest.Write(b)
	n := len(b)
	if o.b.Len()+n > maxOutput {
		o.exceeded = true
		if left := maxOutput - o.b.Len(); left > 0 {
			o.b.Write(b[:left])
		}
		return n, nil
	}
	o.b.Write(b)
	return n, nil
}

func (o *output) sum() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.digest == nil {
		o.digest = sha256.New()
	}
	return o.digest.Sum(nil)
}
