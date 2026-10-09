import unittest

from app.inventory import Stock


class StockTest(unittest.TestCase):
    def test_reserve(self):
        s = Stock({"sku": 3})
        self.assertTrue(s.reserve("sku", 2))
        self.assertFalse(s.reserve("sku", 2))
        self.assertEqual(s.levels["sku"], 1)
