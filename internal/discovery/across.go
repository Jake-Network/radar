package discovery

import (
	"sort"

	"github.com/Jake-Network/radar/internal/model"
)

// MatchAcross proposes relationships between separately discovered repositories.
// Only equal literal paths and known HTTP methods match. Source type names and
// field overlap cannot establish a relationship. Reports and manifests are not
// mutated, and every returned relationship remains proposed evidence.
func MatchAcross(producer Report, consumer Report) []Candidate {
	schemas := make(map[string]Schema, len(producer.Schemas))
	for _, schema := range producer.Schemas {
		schemas[schema.ID] = schema
	}
	result := []Candidate{}
	seen := map[string]bool{}
	for _, endpoint := range producer.Candidates {
		if endpoint.Endpoint == "" || endpoint.Method == "" {
			continue
		}
		schema, ok := schemas[endpoint.Contract]
		if !ok {
			continue
		}
		for _, use := range consumer.Consumers {
			if use.Endpoint != endpoint.Endpoint || use.Method == "" || use.Method != endpoint.Method {
				continue
			}
			id := model.StableID(endpoint.Contract, endpoint.Producer, use.Path, use.Type, endpoint.Endpoint, endpoint.Method)
			if seen[id] {
				continue
			}
			seen[id] = true
			fields := append([]string{}, use.Fields...)
			sort.Strings(fields)
			locations := append([]model.Provenance{}, endpoint.Locations...)
			locations = append(locations, use.Locations...)
			result = append(result, Candidate{
				ID: id, Producer: endpoint.Producer, Consumer: use.Path,
				Contract: endpoint.Contract, SchemaPath: schema.Path, Pointer: schema.Pointer,
				Endpoint: endpoint.Endpoint, Method: endpoint.Method, Fields: fields,
				Evidence: model.Proposed, Locations: locations,
				Ambiguities: []string{"Equal literal path and method propose a cross-repository relationship; deployment origin and runtime compatibility remain unverified."},
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
