package uniqueness

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"text/scanner"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	js "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	py "github.com/tree-sitter/tree-sitter-python/bindings/go"
	ts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type typedNameCandidate struct {
	kind, name string
	line       int
}

// These are advisory syntax observations. Registration scope and permitted
// reuse require explicit declarations; names alone cannot establish a failure.
func typedCandidates(ctx context.Context, file string, b []byte) ([]typedNameCandidate, error) {
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("source exceeds candidate limit")
	}
	if path.Ext(file) == ".prisma" {
		return prismaTableCandidates(b)
	}
	var grammar *sitter.Language
	switch path.Ext(file) {
	case ".py":
		grammar = sitter.NewLanguage(py.Language())
	case ".java":
		grammar = sitter.NewLanguage(java.Language())
	case ".ts":
		grammar = sitter.NewLanguage(ts.LanguageTypescript())
	case ".js":
		grammar = sitter.NewLanguage(js.Language())
	default:
		return nil, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return nil, err
	}
	parser.SetTimeoutMicros(2_000_000)
	tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
		if bounded.Err() != nil || offset >= len(b) {
			return nil
		}
		return b[offset:min(offset+4096, len(b))]
	}, nil, nil)
	if tree == nil {
		return nil, fmt.Errorf("candidate parse incomplete")
	}
	defer tree.Close()
	if bounded.Err() != nil {
		return nil, bounded.Err()
	}
	root := tree.RootNode()
	if root.HasError() {
		return nil, fmt.Errorf("candidate syntax invalid")
	}
	framework := map[string]bool{}
	var walk func(*sitter.Node, func(*sitter.Node))
	walk = func(n *sitter.Node, fn func(*sitter.Node)) {
		fn(n)
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i), fn)
		}
	}
	walk(root, func(n *sitter.Node) {
		if strings.HasPrefix(n.Kind(), "import") {
			text := n.Utf8Text(b)
			for _, name := range []string{"sqlalchemy", "django", "celery", "typeorm", "jakarta.persistence", "javax.persistence"} {
				if strings.Contains(text, name) {
					framework[name] = true
				}
			}
		}
	})
	apps := map[string]bool{}
	walk(root, func(n *sitter.Node) {
		if n.Kind() != "assignment" || !framework["celery"] {
			return
		}
		left, right := n.ChildByFieldName("left"), n.ChildByFieldName("right")
		if left == nil || right == nil || left.Kind() != "identifier" || right.Kind() != "call" {
			return
		}
		fn := right.ChildByFieldName("function")
		if fn != nil && (fn.Utf8Text(b) == "Celery" || fn.Utf8Text(b) == "celery.Celery") {
			apps[left.Utf8Text(b)] = true
		}
	})
	inClass := func(n *sitter.Node) bool {
		for p := n.Parent(); p != nil; p = p.Parent() {
			if p.Kind() == "class_definition" {
				return true
			}
			if p.Kind() == "function_definition" {
				return false
			}
		}
		return false
	}
	var out []typedNameCandidate
	add := func(kind, name string, n *sitter.Node) {
		if name != "" && len(name) <= 256 {
			out = append(out, typedNameCandidate{kind, name, int(n.StartPosition().Row) + 1})
		}
	}
	walk(root, func(n *sitter.Node) {
		switch n.Kind() {
		case "assignment":
			left, right := n.ChildByFieldName("left"), n.ChildByFieldName("right")
			if left == nil || right == nil {
				return
			}
			key := left.Utf8Text(b)
			if key == "__tablename__" && framework["sqlalchemy"] && inClass(n) {
				add("table", candidateLiteral(right, b), n)
			}
			if key == "db_table" && framework["django"] && inClass(n) {
				add("table", candidateLiteral(right, b), n)
			}
		case "call", "call_expression":
			function, args := n.ChildByFieldName("function"), n.ChildByFieldName("arguments")
			if function == nil || args == nil {
				return
			}
			name := function.Utf8Text(b)
			kind := ""
			if framework["celery"] && (name == "shared_task" || strings.HasSuffix(name, ".task") && apps[strings.TrimSuffix(name, ".task")]) {
				kind = "Celery task"
			}
			if framework["django"] && (name == "path" || name == "re_path" || name == "django.urls.path" || name == "django.urls.re_path") {
				kind = "Django URL name"
			}
			if framework["typeorm"] && name == "Entity" {
				for i := uint(0); i < args.NamedChildCount(); i++ {
					v := args.NamedChild(i)
					if lit := candidateLiteral(v, b); lit != "" {
						add("table", lit, n)
					}
					if v.Kind() == "object" {
						walk(v, func(pair *sitter.Node) {
							if pair.Kind() == "pair" {
								k, val := pair.ChildByFieldName("key"), pair.ChildByFieldName("value")
								if k != nil && val != nil && k.Utf8Text(b) == "name" {
									add("table", candidateLiteral(val, b), n)
								}
							}
						})
					}
				}
			}
			if kind != "" {
				for i := uint(0); i < args.NamedChildCount(); i++ {
					arg := args.NamedChild(i)
					if arg.Kind() != "keyword_argument" {
						continue
					}
					k, v := arg.ChildByFieldName("name"), arg.ChildByFieldName("value")
					if k != nil && v != nil && k.Utf8Text(b) == "name" {
						add(kind, candidateLiteral(v, b), n)
					}
				}
			}
		case "annotation":
			if !framework["jakarta.persistence"] && !framework["javax.persistence"] {
				return
			}
			name := n.ChildByFieldName("name")
			if name == nil || name.Utf8Text(b) != "Table" {
				return
			}
			walk(n, func(pair *sitter.Node) {
				if pair.Kind() == "element_value_pair" {
					key, val := pair.ChildByFieldName("key"), pair.ChildByFieldName("value")
					if key != nil && val != nil && key.Utf8Text(b) == "name" {
						add("table", candidateLiteral(val, b), n)
					}
				}
			})
		}
	})
	return out, nil
}
func candidateLiteral(n *sitter.Node, b []byte) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "string", "string_literal":
	default:
		return ""
	}
	s := n.Utf8Text(b)
	if len(s) < 2 || s[0] != s[len(s)-1] || (s[0] != '\'' && s[0] != '"') || strings.ContainsAny(s[1:len(s)-1], "\\\n\r") {
		return ""
	}
	if strings.HasPrefix(s, "\"\"\"") || strings.HasPrefix(s, "'''") {
		return ""
	}
	return s[1 : len(s)-1]
}

// Prisma has no embedded grammar. A bounded token reader handles only literal
// @@map declarations directly within a model block; observations stay advisory.
func prismaTableCandidates(b []byte) ([]typedNameCandidate, error) {
	var scan scanner.Scanner
	scan.Init(strings.NewReader(string(b)))
	scan.Mode = scanner.ScanIdents | scanner.ScanStrings | scanner.ScanComments | scanner.SkipComments
	malformed := false
	scan.Error = func(*scanner.Scanner, string) { malformed = true }
	type token struct {
		text string
		line int
	}
	var tokens []token
	for scan.Scan() != scanner.EOF {
		tokens = append(tokens, token{scan.TokenText(), scan.Position.Line})
		if len(tokens) > 100000 {
			return nil, fmt.Errorf("Prisma token limit exceeded")
		}
	}
	if malformed {
		return nil, fmt.Errorf("Prisma tokenization incomplete")
	}
	depth, modelDepth := 0, 0
	var out []typedNameCandidate
	for i, t := range tokens {
		if t.text == "{" {
			depth++
			if i >= 2 && tokens[i-2].text == "model" {
				modelDepth = depth
			}
		}
		if t.text == "}" {
			if modelDepth == depth {
				modelDepth = 0
			}
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("Prisma braces incomplete")
			}
		}
		if modelDepth > 0 && depth == modelDepth && t.text == "@" && i+5 < len(tokens) && tokens[i+1].text == "@" && tokens[i+2].text == "map" && tokens[i+3].text == "(" && tokens[i+5].text == ")" {
			name, err := strconv.Unquote(tokens[i+4].text)
			if err == nil && name != "" && len(name) <= 256 {
				out = append(out, typedNameCandidate{"table", name, t.line})
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("Prisma braces incomplete")
	}
	return out, nil
}
