# Demos

Each script builds a throwaway Git repository from checked-in fixtures, runs
Radar against it and checks the results. Nothing touches your checkout. CI runs
`scripts/smoke.sh` and every `examples/*/demo.sh`.

Build first (Go 1.23+, CGO and a C compiler):

```sh
go build -o /tmp/radar ./cmd/radar
make demo-all        # builds bin/radar and runs every examples/*/demo.sh
```

The scripts need Bash, Git and Python 3. Read them before running: the
`--run` steps execute the fixture's tests.

## Two green branches, one broken merge

```sh
bash scripts/smoke.sh /tmp/radar
bash examples/integration/demo.sh /tmp/radar
```

One branch doubles the unit price and the other doubles the quantity. Each
passes the budget test on its own, they merge without a conflict, and the
combined tree fails the test. Radar reports the failing case, the source
location and the branch to look at, then verifies the repaired combination.
`smoke.sh` is the short, portable version.

## Selection across languages

```sh
bash examples/intelligent-verification/demo.sh /tmp/radar
```

A Python backend, FastAPI/Pydantic adapter source, TypeScript types, a
JavaScript client and an HTTP integration suite. Radar selects tests across the
language boundary and runs them on the combined tree. Needs Node with `fetch`,
GNU `/usr/bin/time` and permission to open a loopback socket. Nothing is
downloaded.

## Contract gate and selection modes

```sh
bash examples/selection-eval/demo.sh /tmp/radar
```

Shows that deleting the contract manifest cannot pass a contract gate, that an
explicit retirement can, and that grouped targeted suites catch real failures.

## Indexing, plans and contract conflicts

```sh
bash examples/organization/demo.sh /tmp/radar
```

Indexes a TypeScript, Python, Go and Rust fixture, preflights a good plan and a
deliberately broken one, then renames `total` to `total_cents` on a producer
branch while a consumer branch still reads `total`. `radar scan` reports the
removed field and the declared consumer. The script keeps its temporary
repository for inspection.

## Commit-bound test evidence

```sh
bash examples/verification/demo.sh /tmp/radar
```

Records a review declaration, runs a real unittest with
`radar test --allow-execution` and verifies the implementation commit. It then
breaks the producer and shows that the old passing record cannot verify the
new commit, and that an edited plan invalidates its old review.

## Several repositories

```sh
bash scripts/demo_workspace_links.sh /tmp/radar
```

Creates two repositories, declares a cross-repository link, and shows a static
pass, a failure after the producer drops a field, and the repaired pass.
