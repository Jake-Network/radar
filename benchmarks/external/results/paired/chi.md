# External Radar evaluation

Project: go-chi/chi
Revision: `67be7d9cafdaeb4e04e887ff78d09e030ee43b00`

Execution: completed

Kind: paired_evidence

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| chi-controlled-source | failed | False | fail | fail | fail | completed |  | No independent per-file oracle declared. |
| chi-docs-control | passed | False | blocked | blocked | pass | completed |  | No independent per-file oracle declared. |
| chi-true-client-integration | failed | True | fail | fail | fail | completed |  | No independent per-file oracle declared. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 3 | 1/1 | 2/2 | 0/2 | 0/1 | 1/1 | unavailable | unavailable |
| balanced | 3 | 1/1 | 2/2 | 0/2 | 0/1 | 1/1 | unavailable | unavailable |
| full | 3 | 1/1 | 2/2 | 0/2 | 0/1 | 0/1 | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| chi-controlled-source | targeted | first_mode_run | 69.9629 | 26.0959 | 1.4146 | 33.834 | 128872 |
| chi-controlled-source | targeted | warm_tool_cache | 73.0341 | 26.0959 | 1.4146 | 35.959 | 128760 |
| chi-controlled-source | balanced | first_mode_run | 69.9591 | 26.0959 | 2.1477 | 34.749 | 129888 |
| chi-controlled-source | balanced | warm_tool_cache | 69.1921 | 26.0959 | 2.1477 | 34.384 | 131660 |
| chi-controlled-source | full | first_mode_run | 55.2944 | 26.0959 | 1.5051 | 27.316 | 131708 |
| chi-controlled-source | full | warm_tool_cache | 54.0847 | 26.0959 | 1.5051 | 26.782 | 127732 |
| chi-docs-control | targeted | first_mode_run | 0.4267 | 25.9247 | 1.0966 | unavailable | 21388 |
| chi-docs-control | targeted | warm_tool_cache | 0.427 | 25.9247 | 1.0966 | unavailable | 22372 |
| chi-docs-control | balanced | first_mode_run | 0.4268 | 25.9247 | 1.0959 | unavailable | 22136 |
| chi-docs-control | balanced | warm_tool_cache | 0.407 | 25.9247 | 1.0959 | unavailable | 21352 |
| chi-docs-control | full | first_mode_run | 27.2876 | 25.9247 | 1.1168 | 26.861 | 128520 |
| chi-docs-control | full | warm_tool_cache | 27.2343 | 25.9247 | 1.1168 | 26.796 | 128416 |
| chi-true-client-integration | targeted | first_mode_run | 105.5446 | 27.8941 | 1.1016 | 26.189 | 132348 |
| chi-true-client-integration | targeted | warm_tool_cache | 105.4392 | 27.8941 | 1.1016 | 26.189 | 131924 |
| chi-true-client-integration | balanced | first_mode_run | 107.5631 | 27.8941 | 1.5311 | 26.541 | 132200 |
| chi-true-client-integration | balanced | warm_tool_cache | 107.3892 | 27.8941 | 1.5311 | 26.604 | 128480 |
| chi-true-client-integration | full | first_mode_run | 111.3605 | 27.8941 | 1.5651 | 27.324 | 131772 |
| chi-true-client-integration | full | warm_tool_cache | 111.2771 | 27.8941 | 1.5651 | 27.585 | 129024 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
