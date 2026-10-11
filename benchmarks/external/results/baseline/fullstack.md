# External Radar evaluation

Project: FastAPI full-stack template
Revision: `7257e606cb94eada1dca40173e2b7f99dd549bc6`

Execution: completed

| Case | Ground truth | Integration only | Targeted | Balanced | Full |
| --- | --- | --- | --- | --- | --- |
| fullstack-docs-control | unavailable (environment_unavailable) | False | unavailable | unavailable | unavailable |

| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| targeted | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| balanced | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |
| full | 0 | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable | unavailable |

| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Execution s | Peak process RSS KiB |
| --- | --- | --- | --- | --- | --- | --- | --- |
| fullstack-docs-control | targeted | first_mode_run | 0.0624 | 0.5025 | 3.2341 | unavailable | 11564 |
| fullstack-docs-control | balanced | first_mode_run | 0.0832 | 0.5025 | 3.9485 | unavailable | 11264 |
| fullstack-docs-control | full | first_mode_run | 0.1027 | 0.5025 | 4.7022 | unavailable | 11872 |

Small controlled samples do not establish general reliability. Null rates have no usable denominator.
Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.
