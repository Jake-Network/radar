# Full indexing measurements

Local Linux measurements collected during the agent-native milestone on
2026-10-09. Each command ran full parsing plus SQLite snapshot persistence,
without incremental indexing or analysis caching. A built CLI used
`index --summary --json --root FIXTURE`, measured with `/usr/bin/time`.

| Fixture | Indexed files | Entities / edges | Three elapsed times (s) | Max RSS (KiB) |
| --- | ---: | --- | --- | --- |
| Radar source copy at measurement | 109 | 844 / 1,860 | 0.37, 0.36, 0.38 | 35,968; 34,368; 33,672 |
| Generated TS relative-import chain | 1,000 | 4,000 / 4,998 | 0.22, 0.21, 0.20 | 54,596; 47,708; 49,660 |
| Multilingual organization fixture | 10 | 47 / 56 | 0.02, 0.01, 0.01 | 16,324; 16,324; 16,196 |

All nine runs returned zero; manifest-free fixtures produced the expected
informational missing-manifest diagnostics. Raw local measurement artifacts:
`/tmp/radar-performance/results.json` (not a portable repository artifact).

These are small sources copied onto Linux `/tmp`, repeated with warm filesystem
caches and 0.01-second timer resolution. They do not establish large-monorepo
scalability, cold-start cost, Windows-mounted filesystem speed, contract
discovery throughput or near-budget behavior. Subsecond indexing here does
not justify caching/storage invalidation complexity. Broader benchmarks remain
a prerequisite for incremental indexing and storage redesign.
