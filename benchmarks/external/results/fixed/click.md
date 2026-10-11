# External Radar evaluation

Project: Pallets Click
Revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | failed | False | fail | fail | fail | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-docs-only | passed | False | blocked | blocked | pass | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-large-integer-integration | failed | True | fail | fail | fail | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-equivalent-clamp-control | passed | False | pass | pass | pass | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | unavailable | unavailable |
| balanced | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | unavailable | unavailable |
| full | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 0/2 | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | targeted | first_mode_run | 28.8746 | 12.6955 | 5.3988 | 12.828 | 140824 |
| click-intrange-clamp | targeted | warm_tool_cache | 28.3883 | 12.6955 | 5.3988 | 12.927 | 140904 |
| click-intrange-clamp | balanced | first_mode_run | 26.584 | 12.6955 | 5.4456 | 12.102 | 140920 |
| click-intrange-clamp | balanced | warm_tool_cache | 26.3152 | 12.6955 | 5.4456 | 12.122 | 140872 |
| click-intrange-clamp | full | first_mode_run | 23.887 | 12.6955 | 4.8361 | 11.165 | 141128 |
| click-intrange-clamp | full | warm_tool_cache | 22.7385 | 12.6955 | 4.8361 | 10.446 | 141068 |
| click-docs-only | targeted | first_mode_run | 2.2139 | 9.8572 | 4.2246 | unavailable | 46432 |
| click-docs-only | targeted | warm_tool_cache | 2.3379 | 9.8572 | 4.2246 | unavailable | 44896 |
| click-docs-only | balanced | first_mode_run | 2.1802 | 9.8572 | 4.5317 | unavailable | 50944 |
| click-docs-only | balanced | warm_tool_cache | 2.1758 | 9.8572 | 4.5317 | unavailable | 49412 |
| click-docs-only | full | first_mode_run | 12.7076 | 9.8572 | 4.3404 | 10.452 | 140824 |
| click-docs-only | full | warm_tool_cache | 12.6537 | 9.8572 | 4.3404 | 10.406 | 140876 |
| click-large-integer-integration | targeted | first_mode_run | 68.4118 | 11.0932 | 6.78 | 16.308 | 141356 |
| click-large-integer-integration | targeted | warm_tool_cache | 67.4205 | 11.0932 | 6.78 | 16.668 | 141388 |
| click-large-integer-integration | balanced | first_mode_run | 64.2931 | 11.0932 | 6.3828 | 15.14 | 141024 |
| click-large-integer-integration | balanced | warm_tool_cache | 61.9057 | 11.0932 | 6.3828 | 14.373 | 141252 |
| click-large-integer-integration | full | first_mode_run | 51.9559 | 11.0932 | 5.2005 | 12.532 | 141152 |
| click-large-integer-integration | full | warm_tool_cache | 47.9632 | 11.0932 | 5.2005 | 11.153 | 141000 |
| click-equivalent-clamp-control | targeted | first_mode_run | 15.3685 | 14.3923 | 4.9562 | 12.783 | 141140 |
| click-equivalent-clamp-control | targeted | warm_tool_cache | 14.7571 | 14.3923 | 4.9562 | 12.016 | 140608 |
| click-equivalent-clamp-control | balanced | first_mode_run | 15.5926 | 14.3923 | 5.0615 | 12.611 | 140976 |
| click-equivalent-clamp-control | balanced | warm_tool_cache | 14.5746 | 14.3923 | 5.0615 | 12.091 | 141092 |
| click-equivalent-clamp-control | full | first_mode_run | 13.9574 | 14.3923 | 5.4132 | 11.424 | 140840 |
| click-equivalent-clamp-control | full | warm_tool_cache | 15.4762 | 14.3923 | 5.4132 | 13.065 | 140736 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
