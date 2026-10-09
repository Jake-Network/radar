# Pallets Click mutation evaluation

| Mutation | Category | Failing files | targeted selected / missed | balanced selected / missed |
| --- | --- | --- | --- | --- |
| click-intrange-clamp | python-library | tests/test_types/test_IntRange.py | 43/43 / - | 43/43 / - |
| click-normalize-opt | python-library | tests/test_normalization.py | 43/43 / - | 43/43 / - |
| click-confirm-y | python-library | unavailable: suite timed out | - | - |
| click-term-len | python-library | tests/test_compat.py, tests/test_formatting.py | 43/43 / - | 43/43 / - |
| click-auto-envvar | python-library | tests/test_context.py, tests/test_options.py | 43/43 / - | 43/43 / - |
| click-split-args | python-library | (none) | 43/43 / - | 43/43 / - |
| click-test-only | test-change | tests/test_basic.py | 1/43 / - | 1/43 / - |
| click-docs-only | unrelated | (none) | 0/43 / - | 0/43 / - |

```json
{
  "ground_truth_unavailable": [
    "click-confirm-y"
  ],
  "targeted": {
    "mutations": 7,
    "mutations_with_failures": 5,
    "mutations_fully_detected": 5,
    "failing_file_recall": 1.0,
    "selected_files": 216,
    "selected_failing_files": 7,
    "precision": 0.032,
    "execution_reduction": 0.282,
    "false_negative_mutations": [],
    "selections_without_observed_failure": [
      "click-split-args"
    ],
    "fallback_flagged": [],
    "median_selection_seconds": 4.042
  },
  "balanced": {
    "mutations": 7,
    "mutations_with_failures": 5,
    "mutations_fully_detected": 5,
    "failing_file_recall": 1.0,
    "selected_files": 216,
    "selected_failing_files": 7,
    "precision": 0.032,
    "execution_reduction": 0.282,
    "false_negative_mutations": [],
    "selections_without_observed_failure": [
      "click-split-args"
    ],
    "fallback_flagged": [],
    "median_selection_seconds": 3.996
  }
}
```
