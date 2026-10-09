# Native release packaging

From a reviewed checkout with Go 1.23+, a C compiler, Python 3, GNU tar and gzip:

```sh
go test ./...
go vet ./...
go test -race ./...
go build -o /tmp/radar ./cmd/radar
bash examples/organization/demo.sh /tmp/radar
bash examples/verification/demo.sh /tmp/radar
bash scripts/release.sh /tmp/radar-release
```

The helper builds for the current Go host target and packages `radar`, project
license, README, documentation, integration skill, clean examples, schemas, dependency inventory and license notices copied from exact
module directories contributing packages to the executable. GNU tar ordering and fixed archive timestamps,
`gzip -n`, `-trimpath`, disabled VCS stamping and disabled Go build ID reduce
incidental build differences. Reproducibility requires the same source,
toolchain, C compiler, target and dependencies; the helper is not a claim of
cross-toolchain reproducibility. Set `SOURCE_DATE_EPOCH` to a fixed release time
if desired. The checksum file permits integrity checking after distribution.

Inspect dependency notices before publishing. This is packaging only: it does
not upload, tag, push, sign or create a GitHub release. The initial tested target
is Linux amd64. Other platforms require native build and execution validation;
CGO parsers preclude pretending that a pure-Go cross-build validates support.

To inspect a packaged build, extract it into a new directory and run its bundled
examples with the absolute binary path:

```sh
tar -xzf /tmp/radar-release/radar-linux_amd64.tar.gz -C /tmp
/tmp/radar-linux_amd64/radar doctor --root /tmp/radar-linux_amd64/examples/organization --json
bash /tmp/radar-linux_amd64/examples/verification/demo.sh /tmp/radar-linux_amd64/radar
```

Do not overwrite an existing installation until you have checked the checksum
and validated the target platform. Executable scripts in examples run only when
you invoke them explicitly.

The Linux binary uses CGO and the native system C runtime. Distribute it only to compatible architecture and libc targets; verify runtime dependencies when producing an artifact for another host.
