# External Radar evaluation

Project: go-chi/chi
Revision: `67be7d9cafdaeb4e04e887ff78d09e030ee43b00`

Execution: completed

Kind: oracle_only

| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| chi-controlled-source | failed | False | not requested | not requested | not requested | unavailable |  | No independent per-file oracle declared. |
| chi-docs-control | passed | False | not requested | not requested | not requested | unavailable |  | No independent per-file oracle declared. |
| chi-true-client-integration | failed | True | not requested | not requested | not requested | unavailable |  | No independent per-file oracle declared. |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 3 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| balanced | 3 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| full | 3 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
