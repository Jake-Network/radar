import re
import unittest
from pathlib import Path
from backend import product_response

class CheckoutIntegration(unittest.TestCase):
    def test_combined_budget(self):
        quantity = int(re.search(r"QUANTITY = (\d+)", Path("frontend.ts").read_text())[1])
        self.assertLessEqual(product_response()["unit_price"] * quantity, 2)
