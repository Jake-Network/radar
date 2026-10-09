package jsonptr

import "testing"

func TestLookup(t *testing.T) {
	doc := map[string]any{"a/b": map[string]any{"~k": []any{"x", map[string]any{"y": 1.0}}}}
	v, found, err := Lookup(doc, "/a~1b/~0k/1/y")
	if err != nil || !found || v != 1.0 {
		t.Fatal(v, found, err)
	}
	if _, found, err = Lookup(doc, "/missing"); err != nil || found {
		t.Fatal("missing member reported present")
	}
	if _, found, _ = Lookup(doc, "/a~1b/~0k/01"); found {
		t.Fatal("leading-zero index accepted")
	}
	if _, _, err = Lookup(doc, "/a~1b/~0k/0/z"); err != ErrNonContainer {
		t.Fatal("scalar crossing not reported", err)
	}
	for _, bad := range []string{"a", "/~2", "/~"} {
		if Validate(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
	if Escape("a/b~c") != "a~1b~0c" || Unescape("a~1b~0c") != "a/b~c" {
		t.Fatal("escape round trip")
	}
}
