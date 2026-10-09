import unittest
from service import product_response


class ProductUnitTest(unittest.TestCase):
    def test_supported_price_and_shape(self):
        product = product_response()
        self.assertIn(product["unit_price"], [1, 2])
        self.assertEqual(product["currency"], "USD")
        self.assertEqual(product["sku"], "demo-product")
