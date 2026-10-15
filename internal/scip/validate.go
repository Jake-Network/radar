package scip

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// This wire schema follows sourcegraph/scip/scip.proto. The importer deliberately
// consumes only occurrences, but validates known nested metadata too: ignored
// semantics must not conceal malformed protobuf. Unknown fields remain opaque
// wire values for protobuf forward compatibility; length-delimited unknowns may
// be raw bytes rather than messages, so recursively interpreting them is unsafe.
// "v" = varint; "s" = UTF-8 string; "p" = packed/unpacked varints.
var wireSchema = map[string]map[int]string{
	"Index":             {1: "Metadata", 2: "Document", 3: "SymbolInformation"},
	"Metadata":          {1: "v", 2: "ToolInfo", 3: "s", 4: "v"},
	"ToolInfo":          {1: "s", 2: "s", 3: "s"},
	"Document":          {1: "s", 2: "Occurrence", 3: "SymbolInformation", 4: "s", 5: "s", 6: "v"},
	"SymbolInformation": {1: "s", 3: "s", 4: "Relationship", 5: "v", 6: "s", 7: "Signature", 8: "s"},
	"Signature":         {2: "Occurrence", 4: "s", 5: "s"},
	"Relationship":      {1: "s", 2: "v", 3: "v", 4: "v", 5: "v"},
	"Occurrence":        {1: "p", 2: "s", 3: "v", 4: "s", 5: "v", 6: "Diagnostic", 7: "p", 8: "SingleLineRange", 9: "MultiLineRange", 10: "SingleLineRange", 11: "MultiLineRange"},
	"Diagnostic":        {1: "v", 2: "s", 3: "s", 4: "s", 5: "p"},
	"SingleLineRange":   {1: "v", 2: "v", 3: "v"},
	"MultiLineRange":    {1: "v", 2: "v", 3: "v", 4: "v"},
}

func validateWire(data []byte, message string, depth int, budget *int) error {
	if depth > 8 {
		return fmt.Errorf("SCIP protobuf nesting limit exceeded")
	}
	fs, err := fields(data)
	if err != nil {
		return err
	}
	*budget -= len(fs)
	if *budget < 0 {
		return fmt.Errorf("SCIP protobuf total field budget exceeded")
	}
	for _, f := range fs {
		kind, known := wireSchema[message][f.num]
		if !known {
			continue
		}
		switch kind {
		case "v":
			if f.wire != 0 {
				return fmt.Errorf("SCIP %s field %d has invalid wire type", message, f.num)
			}
		case "s":
			if f.wire != 2 || !utf8.Valid(f.bytes) {
				return fmt.Errorf("SCIP %s field %d is not a UTF-8 string", message, f.num)
			}
		case "p":
			if f.wire == 0 {
				continue
			}
			if f.wire != 2 {
				return fmt.Errorf("SCIP packed field has invalid wire type")
			}
			data := f.bytes
			for len(data) > 0 {
				_, n := binary.Uvarint(data)
				if n <= 0 {
					return fmt.Errorf("SCIP packed field has malformed varint")
				}
				data = data[n:]
				*budget--
				if *budget < 0 {
					return fmt.Errorf("SCIP protobuf total field budget exceeded")
				}
			}
		default:
			if f.wire != 2 {
				return fmt.Errorf("SCIP %s field %d is not a message", message, f.num)
			}
			if err := validateWire(f.bytes, kind, depth+1, budget); err != nil {
				return err
			}
		}
	}
	return nil
}
