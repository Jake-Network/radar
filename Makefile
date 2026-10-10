.PHONY: build test check demo demo-all release
build:
	go build -o bin/radar ./cmd/radar
test:
	go test ./...
check:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
	go test -race ./...
demo: build
	bin/radar init --root examples/organization
	bin/radar doctor --root examples/organization --json
	bin/radar index --root examples/organization
	bin/radar graph --root examples/organization --kind schema_field --json

demo-all: build
	bash examples/organization/demo.sh "$$(pwd)/bin/radar"
	bash examples/verification/demo.sh "$$(pwd)/bin/radar"
	bash examples/integration/demo.sh "$$(pwd)/bin/radar"
	bash examples/intelligent-verification/demo.sh "$$(pwd)/bin/radar"
	bash examples/selection-eval/demo.sh "$$(pwd)/bin/radar"
	bash examples/java-maven/demo.sh "$$(pwd)/bin/radar"
release:
	bash scripts/release.sh dist
