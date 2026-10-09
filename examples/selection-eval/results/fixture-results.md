# Fixture mutation evaluation

| Mutation | Category | Failing files | targeted selected / missed | balanced selected / missed |
| --- | --- | --- | --- | --- |
| py-discount-rounding | python-backend | services/api/tests/test_pricing.py | 4/13 / - | 6/13 / - |
| py-line-total | python-backend | services/api/tests/test_orders.py, services/api/tests/test_pricing.py | 4/13 / - | 6/13 / - |
| py-inventory-leak | python-backend | services/api/tests/test_inventory.py | 1/13 / - | 4/13 / - |
| py-serializer-order | python-backend | services/api/tests/test_serializers.py | 1/13 / - | 4/13 / - |
| api-currency-rename | shared-api-schema | services/api/tests/test_orders.py, web/test/contract.integration.test.js | 4/13 / - | 7/13 / - |
| schema-items-type | shared-api-schema | integration/test_order_contract.py | 3/13 / - | 3/13 / - |
| js-format-padding | typescript-frontend | web/test/contract.integration.test.js, web/test/format.test.js | 4/13 / - | 5/13 / - |
| js-summary-field | cross-language | web/test/contract.integration.test.js, web/test/summary.test.js | 2/13 / - | 5/13 / - |
| js-theme-contrast | typescript-frontend | web/test/theme.test.js | 1/13 / - | 5/13 / - |
| go-clock-advance | go-package | gosvc/clock/clock_test.go, gosvc/ratelimit/limiter_test.go | 2/13 / - | 2/13 / - |
| go-health-threshold | go-package | gosvc/health/health_test.go | 1/13 / - | 1/13 / - |
| docs-only | unrelated | (none) | 0/13 / - | 0/13 / - |

```json
{
  "ground_truth_unavailable": [],
  "targeted": {
    "mutations": 12,
    "mutations_with_failures": 11,
    "mutations_fully_detected": 11,
    "failing_file_recall": 1.0,
    "selected_files": 27,
    "selected_failing_files": 16,
    "precision": 0.593,
    "execution_reduction": 0.827,
    "false_negative_mutations": [],
    "selections_without_observed_failure": [],
    "fallback_flagged": [],
    "median_selection_seconds": 0.396
  },
  "balanced": {
    "mutations": 12,
    "mutations_with_failures": 11,
    "mutations_fully_detected": 11,
    "failing_file_recall": 1.0,
    "selected_files": 48,
    "selected_failing_files": 16,
    "precision": 0.333,
    "execution_reduction": 0.692,
    "false_negative_mutations": [],
    "selections_without_observed_failure": [],
    "fallback_flagged": [],
    "median_selection_seconds": 0.392
  }
}
```
