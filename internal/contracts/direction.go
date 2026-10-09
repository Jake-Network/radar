package contracts

func schemaType(s map[string]any) string { t, _ := s["type"].(string); return t }

// subset compares only the supported primitive type domains. An empty type is
// unconstrained; integer is a subset of number. Other distinct types are disjoint.
func subset(a, b string) bool { return a == b || b == "" || (a == "integer" && b == "number") }
func classify(c Change, direction string) string {
	if c.Kind == "contract_removed" {
		return "breaking"
	}
	if direction == "" {
		return "risk"
	}
	switch c.Kind {
	case "type_changed":
		if (direction == "request" && subset(c.BeforeType, c.AfterType)) || (direction == "response" && subset(c.AfterType, c.BeforeType)) {
			return "compatible"
		}
		return "breaking"
	case "field_removed":
		if direction == "request" {
			return "risk"
		}
		return "breaking"
	case "required_added":
		if direction == "response" {
			return "compatible"
		}
		return "breaking"
	case "required_removed":
		if direction == "request" {
			return "compatible"
		}
		return "breaking"
	default:
		return "risk"
	}
}
