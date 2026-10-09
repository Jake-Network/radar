import unittest

from app import orders


class OrdersTest(unittest.TestCase):
    def test_summary(self):
        s = orders.summarize("o-1", [(1000, 2), (250, 1)], discount_percent=10)
        self.assertEqual(s, {"id": "o-1", "total": 2025, "currency": "USD", "items": 3})
