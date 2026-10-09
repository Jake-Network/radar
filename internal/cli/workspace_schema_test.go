package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// conforms checks value against the JSON Schema subset the Radar schemas use:
// type, required, properties, items, minItems, enum, const, pattern, minimum
// and local $ref. It keeps the published schema honest without a dependency.
func conforms(root, schema map[string]any, value any, at string) error {
	if ref, ok := schema["$ref"].(string); ok {
		def := root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			def, _ = def[part].(map[string]any)
		}
		if def == nil {
			return fmt.Errorf("%s: unresolved %s", at, ref)
		}
		return conforms(root, def, value, at)
	}
	if c, ok := schema["const"]; ok && fmt.Sprint(c) != fmt.Sprint(value) {
		return fmt.Errorf("%s: %v is not %v", at, value, c)
	}
	if enum, ok := schema["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(value) }) {
		return fmt.Errorf("%s: %v not in %v", at, value, enum)
	}
	switch schema["type"] {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: %T is not an object", at, value)
		}
		required, _ := schema["required"].([]any)
		for _, key := range required {
			if _, ok := obj[key.(string)]; !ok {
				return fmt.Errorf("%s: missing %s", at, key)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, sub := range properties {
			if v, ok := obj[key]; ok {
				if err := conforms(root, sub.(map[string]any), v, at+"."+key); err != nil {
					return err
				}
			}
		}
	case "array":
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: %T is not an array", at, value)
		}
		if min, ok := schema["minItems"].(float64); ok && float64(len(list)) < min {
			return fmt.Errorf("%s: fewer than %v items", at, min)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, v := range list {
				if err := conforms(root, items, v, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: %T is not a string", at, value)
		}
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(s) {
			return fmt.Errorf("%s: %q does not match %s", at, s, p)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: %T is not a boolean", at, value)
		}
	case "integer":
		n, ok := value.(float64)
		if !ok || n != float64(int64(n)) {
			return fmt.Errorf("%s: %v is not an integer", at, value)
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			return fmt.Errorf("%s: %v below %v", at, n, min)
		}
	}
	return nil
}

func TestWorkspaceReportMatchesSchema(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "schemas", "workspace-report.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err = json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	s := newShop(t)
	_, oneOff := gateJSON(t, s.orders, "--with", "../payments")
	s.register()
	s.agent(s.orders, "agent/clash", map[string]string{"api.py": "def total():\n    return 7\n"})
	put(t, s.wt(s.orders, "agent/api"), "wip.txt", "wip\n")
	_, failing := gateJSON(t, s.orders)
	_, again := gateJSON(t, s.payments, "--again", "--run")
	_, partial := gateJSON(t, s.orders, "--only", "payments", "payments:agent/client")
	_, replay := gateJSON(t, s.orders, "--replay", "last")
	gitTest(t, s.payments, "branch", "-m", "main", "develop")
	_, broken := gateJSON(t, s.orders)
	for name, report := range map[string]map[string]any{"one-off": oneOff, "failing": failing, "again --run": again, "partial": partial, "replay": replay, "base error": broken} {
		if err := conforms(schema, schema, report, "$"); err != nil {
			t.Errorf("%s report does not match the schema: %v", name, err)
		}
	}
	// The check itself must reject a report that drifts from the schema.
	delete(failing, "digest")
	repoOf(t, oneOff, "orders")["selection_source"] = "guessed"
	for _, report := range []map[string]any{failing, oneOff} {
		if conforms(schema, schema, report, "$") == nil {
			t.Fatal("schema check accepted a nonconforming report")
		}
	}
}
