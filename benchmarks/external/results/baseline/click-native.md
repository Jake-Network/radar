# External Radar evaluation

Project: Pallets Click
Revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | failed | False | fail | fail | fail | completed |  | available |
| click-docs-only | passed | False | blocked | blocked | blocked | completed |  | available |
| click-large-integer-integration | failed | True | fail | fail | fail | completed |  | available |
| click-equivalent-clamp-control | passed | False | blocked | blocked | blocked | completed |  | available |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/129 |
| balanced | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/129 |
| full | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/172 |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | targeted | first_mode_run | 18.2101 | 15.7454 | 6.5079 | 14.954 | 140956 |
| click-intrange-clamp | targeted | warm_tool_cache | 17.8482 | 15.7454 | 6.5079 | 14.813 | 140916 |
| click-intrange-clamp | balanced | first_mode_run | 17.454 | 15.7454 | 6.0139 | 14.441 | 141020 |
| click-intrange-clamp | balanced | warm_tool_cache | 18.4114 | 15.7454 | 6.0139 | 15.298 | 140972 |
| click-intrange-clamp | full | first_mode_run | 17.0475 | 15.7454 | 5.8661 | 13.996 | 140896 |
| click-intrange-clamp | full | warm_tool_cache | 15.3695 | 15.7454 | 5.8661 | 12.627 | 140860 |
| click-docs-only | targeted | first_mode_run | 2.2622 | 12.7355 | 4.7636 | unavailable | 53596 |
| click-docs-only | targeted | warm_tool_cache | 2.1345 | 12.7355 | 4.7636 | unavailable | 50844 |
| click-docs-only | balanced | first_mode_run | 2.1943 | 12.7355 | 4.4173 | unavailable | 44184 |
| click-docs-only | balanced | warm_tool_cache | 2.325 | 12.7355 | 4.4173 | unavailable | 50256 |
| click-docs-only | full | first_mode_run | 16.2118 | 12.7355 | 4.7745 | 13.543 | 141028 |
| click-docs-only | full | warm_tool_cache | 17.9931 | 12.7355 | 4.7745 | 14.811 | 140812 |
| click-large-integer-integration | targeted | first_mode_run | 15.1006 | 13.6575 | 5.5291 | 12.096 | 141200 |
| click-large-integer-integration | targeted | warm_tool_cache | 14.9898 | 13.6575 | 5.5291 | 12.44 | 141076 |
| click-large-integer-integration | balanced | first_mode_run | 13.7953 | 13.6575 | 4.9623 | 11.297 | 140676 |
| click-large-integer-integration | balanced | warm_tool_cache | 15.587 | 13.6575 | 4.9623 | 12.535 | 140948 |
| click-large-integer-integration | full | first_mode_run | 14.1958 | 13.6575 | 4.5081 | 11.772 | 140980 |
| click-large-integer-integration | full | warm_tool_cache | 13.5296 | 13.6575 | 4.5081 | 11.034 | 141028 |
| click-equivalent-clamp-control | targeted | first_mode_run | 18.4824 | 15.5034 | 6.0819 | 15.541 | 140988 |
| click-equivalent-clamp-control | targeted | warm_tool_cache | 18.9363 | 15.5034 | 6.0819 | 15.692 | 141004 |
| click-equivalent-clamp-control | balanced | first_mode_run | 17.9657 | 15.5034 | 6.6186 | 14.746 | 141164 |
| click-equivalent-clamp-control | balanced | warm_tool_cache | 16.8655 | 15.5034 | 6.6186 | 14.071 | 141232 |
| click-equivalent-clamp-control | full | first_mode_run | 16.4167 | 15.5034 | 5.4579 | 13.431 | 141008 |
| click-equivalent-clamp-control | full | warm_tool_cache | 16.7386 | 15.5034 | 5.4579 | 13.726 | 140876 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
