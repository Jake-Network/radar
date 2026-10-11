# External Radar evaluation

Project: Hono
Revision: `73ff6c0e82d66468e28ed439481220f56ab03882`

Execution: completed

Kind: radar_evaluation

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| hono-controlled-source | unavailable (failed) | False | blocked | blocked | blocked | completed | Authoritative native full-suite baseline did not pass. | No independent per-file oracle declared. |
| hono-docs-control | unavailable (passed) | False | blocked | blocked | blocked | completed | Authoritative native full-suite baseline did not pass. | No independent per-file oracle declared. |
| hono-passing-source-control | unavailable (passed) | False | blocked | blocked | blocked | completed | Authoritative native full-suite baseline did not pass. | No independent per-file oracle declared. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| balanced | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| full | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| hono-controlled-source | targeted | first_mode_run | 30.6176 | 105.2193 | 38.4848 | unavailable | 146748 |
| hono-controlled-source | balanced | first_mode_run | 27.2962 | 105.2193 | 34.0623 | unavailable | 112024 |
| hono-controlled-source | full | first_mode_run | 28.1663 | 105.2193 | 32.2664 | unavailable | 102672 |
| hono-docs-control | targeted | first_mode_run | 27.3559 | 133.0306 | 31.8555 | unavailable | 113908 |
| hono-docs-control | balanced | first_mode_run | 27.8381 | 133.0306 | 32.281 | unavailable | 97680 |
| hono-docs-control | full | first_mode_run | 27.8468 | 133.0306 | 32.5188 | unavailable | 148800 |
| hono-passing-source-control | targeted | first_mode_run | 25.782 | 150.8887 | 31.7389 | unavailable | 119136 |
| hono-passing-source-control | balanced | first_mode_run | 31.6099 | 150.8887 | 76.4566 | unavailable | 151480 |
| hono-passing-source-control | full | first_mode_run | 37.3367 | 150.8887 | 42.1344 | unavailable | 139012 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
