"""Cross-language check: the API summary satisfies the shared schema."""
import json
import pathlib
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "services" / "api"))

from app.orders import summarize  # noqa: E402


class OrderContractTest(unittest.TestCase):
    def test_summary_matches_schema(self):
        schema = json.loads((ROOT / "contracts" / "order-summary.schema.json").read_text())
        summary = summarize("o-9", [(300, 3)])
        self.assertEqual(sorted(schema["required"]), sorted(summary))
        types = {"string": str, "integer": int}
        for field, spec in schema["properties"].items():
            self.assertIsInstance(summary[field], types[spec["type"]], field)
