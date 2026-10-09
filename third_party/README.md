# Third-party notices

Radar is MIT licensed. Bundled copies in `licenses/` preserve notices for the
direct Tree-sitter grammars/runtime, modernc SQLite and gopkg.in/yaml.v3
(MIT and Apache-2.0, used to read YAML OpenAPI documents) dependencies. They were copied from the exact module versions in
`go.mod`. SQLite's underlying public-domain notice is also included.

`go.mod` and `go.sum` are the dependency inventory. Release packaging must
include notices for the complete resolved dependency set and native components;
these direct dependency copies alone are not a complete binary license audit.
