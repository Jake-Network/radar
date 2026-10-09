# Radar 0.4 evaluation

Repository: `trusted selection monorepo`

Pinned external revision: `checked-in fixture`

| Case | Ground truth | Classification | Gate | Selected / inventory |
| --- | --- | --- | --- | --- |
| py-discount-rounding | failed | defect_detected | fail | 6 / 13 |
| py-line-total | failed | defect_detected | fail | 6 / 13 |
| py-inventory-leak | failed | defect_detected | fail | 4 / 13 |
| py-serializer-order | failed | defect_detected | fail | 4 / 13 |
| api-currency-rename | failed | defect_detected | fail | 7 / 13 |
| schema-items-type | failed | defect_detected | fail | 3 / 13 |
| js-format-padding | failed | defect_detected | fail | 5 / 13 |
| js-summary-field | failed | defect_detected | fail | 5 / 13 |
| js-theme-contrast | failed | defect_detected | fail | 5 / 13 |
| go-clock-advance | failed | defect_detected | fail | 2 / 13 |
| go-health-threshold | failed | defect_detected | fail | 1 / 13 |
| docs-only | passed | no_defect_observed | blocked | 0 / 13 |

## Environment and ground truth

Platform: `Linux-6.18.40.1-microsoft-standard-WSL2-x86_64-with-glibc2.39`; Python: `3.12.3`.

Radar executable SHA256: `caa84a969dab0f7c24516795d55a9c8aa9e8d10f31273c7ea35a184966421e31`.

Independent complete-suite command definitions:

```json
[
  {
    "cwd": "services/api",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "tests",
      "-p",
      "test_inventory.py"
    ],
    "test_files": [
      "services/api/tests/test_inventory.py"
    ],
    "recognizer": "unittest"
  },
  {
    "cwd": "services/api",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "tests",
      "-p",
      "test_orders.py"
    ],
    "test_files": [
      "services/api/tests/test_orders.py"
    ],
    "recognizer": "unittest"
  },
  {
    "cwd": "services/api",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "tests",
      "-p",
      "test_pricing.py"
    ],
    "test_files": [
      "services/api/tests/test_pricing.py"
    ],
    "recognizer": "unittest"
  },
  {
    "cwd": "services/api",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "tests",
      "-p",
      "test_serializers.py"
    ],
    "test_files": [
      "services/api/tests/test_serializers.py"
    ],
    "recognizer": "unittest"
  },
  {
    "cwd": ".",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "integration",
      "-p",
      "test_order_contract.py"
    ],
    "test_files": [
      "integration/test_order_contract.py"
    ],
    "recognizer": "unittest"
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test",
      "test/cart.test.js"
    ],
    "test_files": [
      "web/test/cart.test.js"
    ],
    "recognizer": "node"
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test",
      "test/contract.integration.test.js"
    ],
    "test_files": [
      "web/test/contract.integration.test.js"
    ],
    "recognizer": "node"
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test",
      "test/format.test.js"
    ],
    "test_files": [
      "web/test/format.test.js"
    ],
    "recognizer": "node"
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test",
      "test/summary.test.js"
    ],
    "test_files": [
      "web/test/summary.test.js"
    ],
    "recognizer": "node"
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test",
      "test/theme.test.js"
    ],
    "test_files": [
      "web/test/theme.test.js"
    ],
    "recognizer": "node"
  },
  {
    "cwd": "gosvc",
    "argv": [
      "go",
      "test",
      "-json",
      "-count=1",
      "./clock"
    ],
    "test_files": [
      "gosvc/clock/clock_test.go"
    ],
    "recognizer": "go"
  },
  {
    "cwd": "gosvc",
    "argv": [
      "go",
      "test",
      "-json",
      "-count=1",
      "./health"
    ],
    "test_files": [
      "gosvc/health/health_test.go"
    ],
    "recognizer": "go"
  },
  {
    "cwd": "gosvc",
    "argv": [
      "go",
      "test",
      "-json",
      "-count=1",
      "./ratelimit"
    ],
    "test_files": [
      "gosvc/ratelimit/limiter_test.go"
    ],
    "recognizer": "go"
  }
]
```

Baseline Git merge + CI command definitions:

```json
[
  {
    "cwd": "services/api",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "tests"
    ],
    "recognizer": "unittest",
    "test_files": [
      "services/api/tests/test_inventory.py",
      "services/api/tests/test_orders.py",
      "services/api/tests/test_pricing.py",
      "services/api/tests/test_serializers.py"
    ]
  },
  {
    "cwd": ".",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "discover",
      "-s",
      "integration"
    ],
    "recognizer": "unittest",
    "test_files": [
      "integration/test_order_contract.py"
    ]
  },
  {
    "cwd": "web",
    "argv": [
      "node",
      "--test"
    ],
    "recognizer": "node",
    "test_files": [
      "web/test/cart.test.js",
      "web/test/contract.integration.test.js",
      "web/test/format.test.js",
      "web/test/summary.test.js",
      "web/test/theme.test.js"
    ]
  },
  {
    "cwd": "gosvc",
    "argv": [
      "go",
      "test",
      "-json",
      "-count=1",
      "./..."
    ],
    "recognizer": "go",
    "test_files": [
      "gosvc/clock/clock_test.go",
      "gosvc/health/health_test.go",
      "gosvc/ratelimit/limiter_test.go"
    ]
  }
]
```

- py-discount-rounding: Discount stops rounding half up. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- py-line-total: Line total adds instead of multiplying (transitively used by orders). Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- py-inventory-leak: Reservation increases stock. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- py-serializer-order: Serializer output is no longer key-sorted. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- api-currency-rename: Producer renames currency to currency_code in code and schema; storefront consumer not updated. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- schema-items-type: Schema-only change: items becomes a string. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- js-format-padding: Money formatting pads cents to one digit. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- js-summary-field: Consumer reads amount instead of the contract's total. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- js-theme-contrast: Contrast colors swapped in an isolated module. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- go-clock-advance: Manual clock advances half the duration (used by ratelimit). Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- go-health-threshold: Health reports down with one dependency up. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.
- docs-only: Documentation-only change. Independent existing per-file fixture tests are the oracle; fixture was historically tuned, not held out.

## Aggregate observations

```json
{
  "samples": 12,
  "ground_truth_observed": 12,
  "unavailable_samples": [],
  "integration_defect_detection_rate": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "integration_eligible_cases": [],
  "mutation_defect_detection_rate": {
    "numerator": 11,
    "denominator": 11,
    "value": 1.0
  },
  "known_failing_candidate_count": 11,
  "known_failing_test_recall": {
    "numerator": 16,
    "denominator": 16,
    "value": 1.0
  },
  "false_positive_rate": {
    "numerator": 0,
    "denominator": 1,
    "value": 0.0
  },
  "false_block_rate": {
    "numerator": 1,
    "denominator": 1,
    "value": 1.0
  },
  "uncovered_change_rate": {
    "numerator": 0,
    "denominator": 13,
    "value": 0.0
  },
  "test_selection_precision": {
    "numerator": 16,
    "denominator": 48,
    "value": 0.3333333333333333
  },
  "test_selection_recall": {
    "numerator": 16,
    "denominator": 16,
    "value": 1.0
  },
  "selected_relative_to_complete_suite": {
    "numerator": 48,
    "denominator": 156,
    "value": 0.3076923076923077
  },
  "radar_analysis_overhead_seconds": [
    0.753703,
    0.673816,
    0.597594,
    0.575238,
    0.6115919999999999,
    0.570207,
    0.604058,
    0.633289,
    0.569653,
    0.570651,
    0.5900190000000001,
    0.580113
  ],
  "total_verification_wall_seconds": [
    0.691383,
    0.649662,
    0.522247,
    0.540445,
    0.778097,
    0.451029,
    0.513246,
    0.548408,
    0.509566,
    0.652608,
    0.613299,
    0.24537
  ],
  "baseline_full_suite_wall_seconds": [
    0.760888,
    0.807846,
    0.681253,
    0.702815,
    0.718144,
    0.659852,
    0.659839,
    0.702492,
    0.659017,
    0.65732,
    0.657353,
    0.681443
  ],
  "peak_memory_kib": [
    17536,
    17536,
    17308,
    17792,
    17568,
    17408,
    17408,
    17536,
    17600,
    17536,
    17344,
    17400
  ],
  "runtime_gate_peak_memory_kib": [
    51756,
    51752,
    18236,
    17792,
    51696,
    51632,
    52544,
    51852,
    52020,
    84444,
    83384,
    17368
  ]
}
```

## Limitations

- Observed passing tests do not establish the absence of defects.
- Recall uses failing test-file groups; Go package commands group files, not individual test cases.
- Selection precision counts selected files observed failing, a lower bound on useful selection.
- GNU time reports per-command maximum RSS; it is not simultaneous aggregate process-tree memory.
- Isolation wrapper and attestation are supplied by the operator; the harness cannot certify isolation.
- Independent branch/full-suite runs and Radar execution are repeated costs; no speed claim follows from selection fraction.

Complete commands, outputs, environment, timing and provenance are in the sibling JSON report.
