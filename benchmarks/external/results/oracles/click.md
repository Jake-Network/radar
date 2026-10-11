# External Radar evaluation

Project: Pallets Click
Revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

Execution: completed

Kind: oracle_only

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| click-intrange-clamp | failed | False | not requested | not requested | not requested | unavailable |  | available |
| click-docs-only | passed | False | not requested | not requested | not requested | unavailable |  | available |
| click-large-integer-integration | failed | True | not requested | not requested | not requested | unavailable |  | available |
| click-equivalent-clamp-control | passed | False | not requested | not requested | not requested | unavailable |  | available |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 4 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| balanced | 4 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| full | 4 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
