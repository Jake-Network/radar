package cli

import (
	"strings"
	"testing"
)

func TestGateProtobufFieldNumberPublicOutputAndExit(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("proto-duplicate", map[string]string{"schema.proto": "syntax = \"proto3\";\nmessage M {\nstring first = 1;\nstring second = 1;\n}\n"})
	before := watchSourceFingerprint(t, root)
	report := invoke(t, root, 1, "gate", "--base", "main", "proto-duplicate")
	if report["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatalf("%+v", report)
	}
	found := false
	for _, raw := range report["findings"].([]any) {
		f := raw.(map[string]any)
		if f["code"] == "unique_protobuf_field_number" {
			found = true
			if f["evidence"] != "verified_static" || len(f["locations"].([]any)) != 2 {
				t.Fatal(f)
			}
		}
	}
	if !found {
		t.Fatal(report)
	}
	code, out, errs := run(t, root, "gate", "--base", "main", "proto-duplicate")
	if code != 1 || !strings.Contains(out, "schema.proto") || !strings.Contains(out, "field tag") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if watchSourceFingerprint(t, root) != before {
		t.Fatal("gate changed source")
	}
}
