package migrations

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	grammar "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var migrationClassTimestamp = regexp.MustCompile(`[0-9]{13}$`)

// Javascript migration files are data, never imported. Explicit locations
// declare the loaded migration scope; computed registration remains outside
// this detector's supported contract.
func (h *history) javascriptIssues(ctx context.Context, l location, files []File, read Reader) error {
	identities := map[string][]File{}
	timestamps := map[string][]File{}
	for _, f := range files {
		if path.Dir(f.Path) != l.dir {
			h.unknown = append(h.unknown, f.Path+": nested JavaScript migration loading is not declared")
			continue
		}
		name := path.Base(f.Path)
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		if l.tool == toolKnex {
			identities[name] = append(identities[name], f)
			if prefix, _, ok := strings.Cut(name, "_"); ok && numericName.MatchString(prefix) {
				timestamps[prefix] = append(timestamps[prefix], f)
			}
			continue
		}
		content, e := read(f.OID)
		if e != nil {
			h.unknown = append(h.unknown, f.Path+": migration source cannot be read")
			continue
		}
		parser := sitter.NewParser()
		if e := parser.SetLanguage(sitter.NewLanguage(grammar.LanguageTypescript())); e != nil {
			parser.Close()
			return e
		}
		parser.SetTimeoutMicros(5_000_000)
		parseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		tree := parser.ParseWithOptions(func(offset int, _ sitter.Point) []byte {
			if parseCtx.Err() != nil || offset >= len(content) {
				return nil
			}
			return content[offset:min(offset+4096, len(content))]
		}, nil, nil)
		parser.Close()
		cancel()
		if e := ctx.Err(); e != nil {
			if tree != nil {
				tree.Close()
			}
			return e
		}
		if tree == nil {
			h.unknown = append(h.unknown, f.Path+": migration syntax analysis did not finish")
			continue
		}
		root := tree.RootNode()
		if root.HasError() {
			h.unknown = append(h.unknown, f.Path+": malformed migration syntax")
			tree.Close()
			continue
		}
		stack := []*sitter.Node{root}
		found := false
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if node.Kind() == "class_declaration" {
				body := node.ChildByFieldName("body")
				className := node.ChildByFieldName("name")
				if body == nil || className == nil {
					h.unknown = append(h.unknown, f.Path+": anonymous migration class")
					continue
				}
				identity := className.Utf8Text(content)
				up, down := false, false
				why := ""
				for i := uint(0); i < body.NamedChildCount(); i++ {
					member := body.NamedChild(i)
					n := member.ChildByFieldName("name")
					if n == nil {
						continue
					}
					key := n.Utf8Text(content)
					if member.Kind() == "method_definition" {
						up = up || key == "up"
						down = down || key == "down"
						if key == "constructor" {
							why = "constructor-defined migration identity is unsupported"
						}
					}
					if key == "name" {
						value := member.ChildByFieldName("value")
						if value == nil || value.Kind() != "string" {
							why = "computed migration name"
						} else {
							text := value.Utf8Text(content)
							if len(text) < 2 || strings.ContainsAny(text[1:len(text)-1], "\\\r\n") {
								why = "escaped migration name is unsupported"
							} else {
								identity = text[1 : len(text)-1]
							}
						}
					}
				}
				if !up || !down {
					continue
				}
				found = true
				if why != "" || !migrationClassTimestamp.MatchString(identity) {
					h.unknown = append(h.unknown, f.Path+": unsupported migration identity "+why)
					continue
				}
				identities[identity] = append(identities[identity], f)
				timestamps[migrationClassTimestamp.FindString(identity)] = append(timestamps[migrationClassTimestamp.FindString(identity)], f)
				continue
			}
			for i := uint(0); i < node.NamedChildCount(); i++ {
				stack = append(stack, node.NamedChild(i))
			}
		}
		if !found {
			h.unknown = append(h.unknown, f.Path+": no literal migration class with up and down methods")
		}
		tree.Close()
	}
	for _, identity := range sortedKeys(keysOf(identities)) {
		if len(identities[identity]) > 1 {
			h.metadataProblem(identity, "Loaded migrations repeat identity "+identity, identities[identity]...)
		}
	}
	for _, timestamp := range sortedKeys(keysOf(timestamps)) {
		if len(timestamps[timestamp]) > 1 {
			h.metadataProblem("timestamp:"+timestamp, fmt.Sprintf("Multiple migrations share timestamp %s; execution order needs review", timestamp), timestamps[timestamp]...)
			h.issues[len(h.issues)-1].Kind = kindRisk
		}
	}
	return nil
}

var numericName = regexp.MustCompile(`^[0-9]+$`)
