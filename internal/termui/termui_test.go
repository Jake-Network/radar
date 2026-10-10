package termui

import (
	"bytes"
	"os"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"auto", "always", "never"} {
		if m, err := ParseMode(s); err != nil || string(m) != s {
			t.Fatalf("%s: %v %v", s, m, err)
		}
	}
	if _, err := ParseMode("yes"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestColorPrecedence(t *testing.T) {
	var buf bytes.Buffer
	cases := []struct {
		name string
		mode Mode
		env  map[string]string
		want bool
	}{
		{"auto buffer is not a terminal", Auto, nil, false},
		{"always wins over NO_COLOR", Always, map[string]string{"NO_COLOR": "1"}, true},
		{"never wins over FORCE_COLOR", Never, map[string]string{"FORCE_COLOR": "1"}, false},
		{"NO_COLOR wins over FORCE_COLOR in auto", Auto, map[string]string{"NO_COLOR": "1", "FORCE_COLOR": "1"}, false},
		{"FORCE_COLOR enables a pipe", Auto, map[string]string{"FORCE_COLOR": "1"}, true},
		{"CLICOLOR_FORCE enables a pipe", Auto, map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"FORCE_COLOR=0 does not", Auto, map[string]string{"FORCE_COLOR": "0"}, false},
		{"TERM=dumb", Auto, map[string]string{"TERM": "dumb"}, false},
	}
	for _, c := range cases {
		if got := Color(&buf, c.mode, env(c.env)); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestFilesAndBuffersAreNotTerminals(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) || IsTerminal(&bytes.Buffer{}) || IsTerminal(nil) || Live(f, env(nil)) {
		t.Fatal("a regular file or buffer was treated as a terminal")
	}
	if null, err := os.Open(os.DevNull); err == nil {
		defer null.Close()
		if IsTerminal(null) {
			t.Fatal("the null device is not a terminal")
		}
	}
}
