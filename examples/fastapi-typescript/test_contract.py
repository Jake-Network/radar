import json
from pathlib import Path
import unittest

class ContractSnapshot(unittest.TestCase):
    def test_email(self):
        schema = json.loads(Path(__file__).with_name("schema.json").read_text())
        self.assertIn("email", schema["properties"]["profile"]["properties"])
