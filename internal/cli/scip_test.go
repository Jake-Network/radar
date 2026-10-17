package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestIndexExplicitSCIPAndStaleBinding(t *testing.T) {
	root, _ := checkoutRepo(t)
	if code, _, errs := run(t, root, "init"); code != 0 {
		t.Fatal(errs)
	}
	ref := gitTest(t, root, "rev-parse", "HEAD")
	field := func(n int, data []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(n<<3|2))
		out = binary.AppendUvarint(out, uint64(len(data)))
		return append(out, data...)
	}
	occ := field(2, []byte("scip-python python example 1 price()."))
	occ = append(occ, []byte{0x18, 1}...)
	occ = append(occ, field(1, []byte{0, 4, 9})...)
	doc := append(field(1, []byte("backend.py")), field(2, occ)...)
	data := append(field(1, nil), field(2, doc)...)
	indexHash := sha256.Sum256(data)
	content := gitTest(t, root, "show", ref+":backend.py") + "\n"
	sourceHash := sha256.Sum256([]byte(content))
	binding := map[string]any{"version": 1, "revision": ref, "index_sha256": hex.EncodeToString(indexHash[:]), "sources": map[string]string{"backend.py": hex.EncodeToString(sourceHash[:])}}
	put(t, root, ".scip/index.scip", string(data))
	b, _ := json.Marshal(binding)
	put(t, root, ".scip/binding.json", string(b))
	report := invoke(t, root, 0, "index", "--ref", ref, "--scip", ".scip/index.scip", "--scip-binding", ".scip/binding.json")
	raw, _ := json.Marshal(report)
	if !strings.Contains(string(raw), "scip-import") || !strings.Contains(string(raw), "verified_tool") {
		t.Fatal(string(raw))
	}
	binding["revision"] = "other"
	b, _ = json.Marshal(binding)
	put(t, root, ".scip/binding.json", string(b))
	stale := invoke(t, root, 2, "index", "--ref", ref, "--scip", ".scip/index.scip", "--scip-binding", ".scip/binding.json")
	if stale["status"] != "unknown" {
		t.Fatal(stale)
	}
	binding["revision"] = ref
	binding["unrecognized"] = true
	b, _ = json.Marshal(binding)
	put(t, root, ".scip/binding.json", string(b))
	if bad := invoke(t, root, 2, "index", "--ref", ref, "--scip", ".scip/index.scip", "--scip-binding", ".scip/binding.json"); bad["status"] != "unknown" {
		t.Fatal(bad)
	}
}
