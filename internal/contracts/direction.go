package contracts

import "strings"

func types(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "|")
}

// subset reports whether every value of type set a is accepted by b. An empty
// set is unconstrained; integer is a subset of number.
func subset(a, b []string) bool {
	if b == nil {
		return true
	}
	if a == nil {
		return false
	}
	in := set(b...)
	for _, t := range a {
		if !in[t] && !(t == "integer" && in["number"]) {
			return false
		}
	}
	return true
}

// classify ranks a change as compatible, breaking or risk for the declared
// direction: request schemas constrain what consumers send, response schemas
// what consumers receive. "unanalyzed" changes are never classified.
func classify(c Change, direction string) string {
	switch c.Kind {
	case "graphql_compatible":
		return "compatible"
	case "contract_removed", "idl_field_removed", "idl_enum_value_removed", "protobuf_number_reused", "protobuf_number_changed", "protobuf_type_changed", "protobuf_reserved_violation", "protobuf_required_added", "graphql_argument_required", "graphql_argument_changed", "graphql_argument_removed", "graphql_type_changed", "graphql_input_tightened", "graphql_output_weakened", "idl_definition_kind_changed", "protobuf_package_changed", "protobuf_enum_number_changed":
		return "breaking"
	case "unanalyzed":
		return "unanalyzed"
	}
	if direction == "" {
		return "risk"
	}
	request := direction == "request"
	pick := func(requestResult, responseResult string) string {
		if request {
			return requestResult
		}
		return responseResult
	}
	switch c.Kind {
	case "type_changed":
		before, after := types(c.BeforeType), types(c.AfterType)
		if (request && subset(before, after)) || (!request && subset(after, before)) {
			return "compatible"
		}
		return "breaking"
	case "field_removed":
		return pick("risk", "breaking")
	case "required_added":
		return pick("breaking", "compatible")
	case "required_removed":
		return pick("compatible", "breaking")
	case "enum_value_added", "enum_removed":
		return pick("compatible", "risk")
	case "enum_value_removed", "enum_added":
		return pick("breaking", "compatible")
	}
	return "risk"
}

func requiredChange(c Change) bool {
	return c.Kind == "required_added" || c.Kind == "protobuf_required_added" || c.Kind == "graphql_input_tightened" || c.Kind == "protobuf_reserved_violation"
}
