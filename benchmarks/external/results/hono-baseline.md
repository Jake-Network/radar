# External Radar evaluation

Project: Hono
Revision: `73ff6c0e82d66468e28ed439481220f56ab03882`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason |
| --- | --- | --- | --- | --- | --- | --- | --- |
| hono-controlled-source | unavailable (environment_unavailable) | False | blocked | blocked | blocked | completed | Baseline full suite or per-file oracle did not pass. |
| hono-docs-control | unavailable (environment_unavailable) | False | blocked | blocked | blocked | completed | Baseline full suite or per-file oracle did not pass. |
| hono-passing-source-control | unavailable (environment_unavailable) | False | blocked | blocked | blocked | completed | Baseline full suite or per-file oracle did not pass. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| balanced | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| full | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| hono-controlled-source | targeted | first_mode_run | 27.2454 | 72.4917 | 33.1369 | unavailable | 148100 |
| hono-controlled-source | balanced | first_mode_run | 28.8822 | 72.4917 | 32.7336 | unavailable | 149320 |
| hono-controlled-source | full | first_mode_run | 33.253 | 72.4917 | 34.8917 | unavailable | 155304 |
| hono-docs-control | targeted | first_mode_run | 29.3028 | 75.7751 | 35.7047 | unavailable | 156236 |
| hono-docs-control | balanced | first_mode_run | 26.1754 | 75.7751 | 33.109 | unavailable | 161376 |
| hono-docs-control | full | first_mode_run | 25.8693 | 75.7751 | 30.0224 | unavailable | 157840 |
| hono-passing-source-control | targeted | first_mode_run | 38.5824 | 75.1646 | 44.4696 | unavailable | 156056 |
| hono-passing-source-control | balanced | first_mode_run | 36.9278 | 75.1646 | 43.5309 | unavailable | 113844 |
| hono-passing-source-control | full | first_mode_run | 35.543 | 75.1646 | 41.4254 | unavailable | 157592 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
