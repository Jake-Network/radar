# Hono dependency preparation

The prepared image reported Node `v20.20.2` in isolated version inspection.

The manifest pins Hono `73ff6c0e82d66468e28ed439481220f56ab03882` (4.6.0).
Download objects with Git as data. Do not run checkout scripts on the host.
Create a temporary build context from `git archive REVISION`, then build:

```sh
git -C /path/to/hono archive 73ff6c0e82d66468e28ed439481220f56ab03882 | tar -x -C /tmp/hono-context
cp benchmarks/external/images/hono.Dockerfile /tmp/hono-context/Dockerfile
docker build -t radar-external-hono:73ff6c0 /tmp/hono-context
docker image inspect radar-external-hono:73ff6c0 --format '{{.Id}}'
python3 benchmarks/external/run.py --manifest benchmarks/external/manifests/hono.json \
  --checkout /path/to/hono --radar /tmp/radar-external-bin \
  --output /tmp/hono-result.json --timeout 600 --repeats 2
```

The Dockerfile pins its base image digest and uses the committed Yarn lockfile
with `--frozen-lockfile --ignore-scripts`. esbuild's install script is the only
explicit dependency setup script, executed in the disposable build. Builds
need package network access; runtime evaluation disables networking and mounts
no credentials or host caches. Record the resulting image ID because Debian
package mirrors can change despite the fixed base and Yarn lockfile.

Vitest writes its results cache below `node_modules/.vite`. The runtime manifest
therefore declares `/opt/hono/node_modules/.vite` as a disposable writable cache
mount; dependencies remain read-only. The worker limits CPU affinity to two
available CPUs so Vitest's default fork count agrees with the Docker CPU budget.
The initial run without these controls is preserved as unavailable ground truth:
cache writes failed and several upstream five-second tests timed out.

The independently executed baseline is upstream `npm test`: TypeScript checking
and all three configured Vitest suites. This is the project's complete core
suite. It excludes the separate `test:all` runtime-adapter matrix requiring
Bun, Deno, Fastly and Cloudflare tools. Results must retain this scope restriction
and must not claim validation of all supported runtimes.

Radar's inferred Vitest command executes its default config only. Candidate
construction drops untracked `node_modules`, so selected commands may block
for unavailable dependencies even when the independent core suite passes.
That is a reported execution limitation, not proof of a safe candidate.
