import unittest

from app.pricing import apply_discount, line_total, subtotal


class PricingTest(unittest.TestCase):
    def test_line_total(self):
        self.assertEqual(line_total(250, 4), 1000)

    def test_discount_rounds_half_up(self):
        self.assertEqual(apply_discount(1999, 10), 1799)

    def test_subtotal(self):
        self.assertEqual(subtotal([(100, 2), (5, 3)]), 215)

    def test_negative_quantity(self):
        with self.assertRaises(ValueError):
            line_total(1, -1)
