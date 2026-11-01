package contracts

import (
	"fmt"
	"strings"
	"unicode"
)

// ProtobufFieldNumber is a literal field tag in one fully qualified message.
// It describes syntax only, not wire compatibility or imported type resolution.
type ProtobufFieldNumber struct {
	Message string
	Number  string
	Line    int
}

// ProtobufFieldNumbers uses the contract lexer and bounded message grammar.
// Unlike contract decoding it retains duplicate tags for the uniqueness check.
// Unsupported declarations return no usable evidence.
func ProtobufFieldNumbers(data []byte) ([]ProtobufFieldNumber, error) {
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("protobuf source exceeds limit")
	}
	tokens, err := lexIDL(data, false)
	if err != nil {
		return nil, err
	}
	if len(tokens) > 100000 {
		return nil, fmt.Errorf("protobuf token limit exceeded")
	}
	lines := idlTokenLines(string(data), tokens)
	var out []ProtobufFieldNumber
	r := idlReader{tokens: tokens}
	r.fieldObserver = func(scope []string, n string, pos int) {
		out = append(out, ProtobufFieldNumber{strings.Join(scope, "."), n, lines[pos]})
	}
	pkg := ""
	seen := map[string]bool{}
	for r.peek() != "" {
		switch r.take() {
		case "syntax":
			if err = r.want("="); err != nil {
				return nil, err
			}
			syntax := r.take()
			if syntax != `"proto2"` && syntax != `"proto3"` {
				return nil, fmt.Errorf("unsupported protobuf syntax")
			}
			if err = r.want(";"); err != nil {
				return nil, err
			}
		case "package":
			pkg, err = r.qualified()
			if err != nil {
				return nil, err
			}
			if err = r.want(";"); err != nil {
				return nil, err
			}
		case "message":
			name := r.take()
			if !idlName(name) || seen[name] {
				return nil, fmt.Errorf("invalid or repeated message declaration")
			}
			seen[name] = true
			r.scope = []string{name}
			if pkg != "" {
				r.scope = []string{pkg, name}
			}
			if _, err = r.protoMessage(0); err != nil {
				return nil, err
			}
		case "enum":
			name := r.take()
			if !idlName(name) || seen[name] {
				return nil, fmt.Errorf("invalid enum declaration")
			}
			seen[name] = true
			if _, err = r.protoEnum(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported protobuf declaration")
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no protobuf declarations")
	}
	return out, nil
}

// lexIDL has already validated each token. Walk the identical whitespace and
// comment boundaries to retain positions without searching inside comments.
func idlTokenLines(src string, tokens []string) []int {
	lines := make([]int, len(tokens))
	offset, line := 0, 1
	for i, token := range tokens {
		for offset < len(src) {
			if unicode.IsSpace(rune(src[offset])) {
				if src[offset] == '\n' {
					line++
				}
				offset++
				continue
			}
			if strings.HasPrefix(src[offset:], "//") {
				for offset < len(src) && src[offset] != '\n' {
					offset++
				}
				continue
			}
			if strings.HasPrefix(src[offset:], "/*") {
				end := strings.Index(src[offset+2:], "*/") + offset + 4
				line += strings.Count(src[offset:end], "\n")
				offset = end
				continue
			}
			break
		}
		lines[i] = line
		line += strings.Count(token, "\n")
		offset += len(token)
	}
	return lines
}
