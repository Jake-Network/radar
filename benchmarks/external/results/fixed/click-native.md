# External Radar evaluation

Project: Pallets Click
Revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | failed | False | fail | fail | fail | completed |  | available |
| click-docs-only | passed | False | blocked | blocked | pass | completed |  | available |
| click-large-integer-integration | failed | True | fail | fail | fail | completed |  | available |
| click-equivalent-clamp-control | passed | False | pass | pass | pass | completed |  | available |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | 2/2 | 2/129 |
| balanced | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | 2/2 | 2/129 |
| full | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 0/2 | 2/2 | 2/172 |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | targeted | first_mode_run | 33.1341 | 15.7311 | 6.6096 | 14.765 | 141004 |
| click-intrange-clamp | targeted | warm_tool_cache | 32.5593 | 15.7311 | 6.6096 | 14.524 | 141020 |
| click-intrange-clamp | balanced | first_mode_run | 31.3926 | 15.7311 | 6.152 | 14.592 | 141088 |
| click-intrange-clamp | balanced | warm_tool_cache | 28.3108 | 15.7311 | 6.152 | 12.596 | 140800 |
| click-intrange-clamp | full | first_mode_run | 26.2093 | 15.7311 | 4.9753 | 11.902 | 141248 |
| click-intrange-clamp | full | warm_tool_cache | 24.4301 | 15.7311 | 4.9753 | 10.844 | 141156 |
| click-docs-only | targeted | first_mode_run | 2.5415 | 13.3942 | 5.0578 | unavailable | 52228 |
| click-docs-only | targeted | warm_tool_cache | 2.8864 | 13.3942 | 5.0578 | unavailable | 49564 |
| click-docs-only | balanced | first_mode_run | 2.4779 | 13.3942 | 5.9982 | unavailable | 45744 |
| click-docs-only | balanced | warm_tool_cache | 2.4783 | 13.3942 | 5.9982 | unavailable | 48076 |
| click-docs-only | full | first_mode_run | 15.6536 | 13.3942 | 5.0966 | 13.126 | 140892 |
| click-docs-only | full | warm_tool_cache | 14.1909 | 13.3942 | 5.0966 | 11.719 | 141248 |
| click-large-integer-integration | targeted | first_mode_run | 54.1963 | 12.6555 | 4.7559 | 11.782 | 141200 |
| click-large-integer-integration | targeted | warm_tool_cache | 67.5844 | 12.6555 | 4.7559 | 16.179 | 141248 |
| click-large-integer-integration | balanced | first_mode_run | 64.0289 | 12.6555 | 6.2777 | 15.749 | 141360 |
| click-large-integer-integration | balanced | warm_tool_cache | 56.4028 | 12.6555 | 6.2777 | 13.65 | 141196 |
| click-large-integer-integration | full | first_mode_run | 54.3326 | 12.6555 | 5.0559 | 12.544 | 141128 |
| click-large-integer-integration | full | warm_tool_cache | 55.8764 | 12.6555 | 5.0559 | 14.057 | 141160 |
| click-equivalent-clamp-control | targeted | first_mode_run | 15.4527 | 11.705 | 4.466 | 13.115 | 140940 |
| click-equivalent-clamp-control | targeted | warm_tool_cache | 16.4647 | 11.705 | 4.466 | 13.89 | 141020 |
| click-equivalent-clamp-control | balanced | first_mode_run | 16.5097 | 11.705 | 5.4972 | 13.561 | 141040 |
| click-equivalent-clamp-control | balanced | warm_tool_cache | 16.0226 | 11.705 | 5.4972 | 13.086 | 141348 |
| click-equivalent-clamp-control | full | first_mode_run | 14.8054 | 11.705 | 5.0311 | 12.239 | 141076 |
| click-equivalent-clamp-control | full | warm_tool_cache | 14.9903 | 11.705 | 5.0311 | 12.454 | 140960 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
