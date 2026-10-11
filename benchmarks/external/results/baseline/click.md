# External Radar evaluation

Project: Pallets Click
Revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | failed | False | fail | fail | fail | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-docs-only | passed | False | blocked | blocked | blocked | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-large-integer-integration | failed | True | fail | fail | fail | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |
| click-equivalent-clamp-control | passed | False | blocked | blocked | blocked | completed |  | Per-file baseline does not pass; native-suite results remain authoritative. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | unavailable | unavailable |
| balanced | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | unavailable | unavailable |
| full | 4 | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | targeted | first_mode_run | 15.8031 | 14.2225 | 6.5669 | 12.835 | 141060 |
| click-intrange-clamp | targeted | warm_tool_cache | 16.4923 | 14.2225 | 6.5669 | 13.763 | 140680 |
| click-intrange-clamp | balanced | first_mode_run | 16.2753 | 14.2225 | 5.4871 | 13.544 | 140952 |
| click-intrange-clamp | balanced | warm_tool_cache | 15.7165 | 14.2225 | 5.4871 | 12.702 | 140900 |
| click-intrange-clamp | full | first_mode_run | 16.2755 | 14.2225 | 5.0985 | 13.354 | 140664 |
| click-intrange-clamp | full | warm_tool_cache | 15.5953 | 14.2225 | 5.0985 | 12.913 | 140952 |
| click-docs-only | targeted | first_mode_run | 2.4562 | 13.0276 | 4.954 | unavailable | 52156 |
| click-docs-only | targeted | warm_tool_cache | 2.376 | 13.0276 | 4.954 | unavailable | 47988 |
| click-docs-only | balanced | first_mode_run | 2.5578 | 13.0276 | 4.84 | unavailable | 51324 |
| click-docs-only | balanced | warm_tool_cache | 2.4584 | 13.0276 | 4.84 | unavailable | 46688 |
| click-docs-only | full | first_mode_run | 12.3325 | 13.0276 | 4.6332 | 10.033 | 140856 |
| click-docs-only | full | warm_tool_cache | 12.7285 | 13.0276 | 4.6332 | 10.614 | 140816 |
| click-large-integer-integration | targeted | first_mode_run | 13.1251 | 9.848 | 4.2239 | 10.946 | 140880 |
| click-large-integer-integration | targeted | warm_tool_cache | 12.7871 | 9.848 | 4.2239 | 10.45 | 141124 |
| click-large-integer-integration | balanced | first_mode_run | 12.7248 | 9.848 | 4.4692 | 10.419 | 141176 |
| click-large-integer-integration | balanced | warm_tool_cache | 12.874 | 9.848 | 4.4692 | 10.585 | 141356 |
| click-large-integer-integration | full | first_mode_run | 13.7023 | 9.848 | 4.473 | 11.28 | 140908 |
| click-large-integer-integration | full | warm_tool_cache | 14.5895 | 9.848 | 4.473 | 12.222 | 141148 |
| click-equivalent-clamp-control | targeted | first_mode_run | 19.732 | 13.4971 | 6.5427 | 16.288 | 141364 |
| click-equivalent-clamp-control | targeted | warm_tool_cache | 20.072 | 13.4971 | 6.5427 | 16.696 | 141272 |
| click-equivalent-clamp-control | balanced | first_mode_run | 18.5989 | 13.4971 | 6.487 | 15.415 | 140912 |
| click-equivalent-clamp-control | balanced | warm_tool_cache | 18.9186 | 13.4971 | 6.487 | 15.672 | 140904 |
| click-equivalent-clamp-control | full | first_mode_run | 18.5093 | 13.4971 | 6.4611 | 15.227 | 141048 |
| click-equivalent-clamp-control | full | warm_tool_cache | 18.9719 | 13.4971 | 6.4611 | 15.786 | 141080 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
