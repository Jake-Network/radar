import unittest

from app.serializers import from_json, to_json


class SerializerTest(unittest.TestCase):
    def test_roundtrip(self):
        value = {"id": "o-1", "total": 5}
        self.assertEqual(from_json(to_json(value)), value)

    def test_stable(self):
        self.assertEqual(to_json({"b": 1, "a": 2}), '{"a":2,"b":1}')
