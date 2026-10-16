"""Exercise bounded native metadata reads without starting agent sessions."""

import json
from pathlib import Path
import unittest
from unittest.mock import patch

from smoke_native import MetadataClient


class MetadataDeadlineTests(unittest.TestCase):
    def client_with_notifications(self):
        client = MetadataClient([], {}, Path("/unused"))
        client.lines.put(json.dumps({"method": "progress"}))
        client.lines.put(json.dumps({"id": 1, "result": {}}))
        return client

    def test_queued_notifications_cannot_extend_deadline(self):
        client = self.client_with_notifications()
        # The first notification arrives within budget, but processing it uses
        # the remaining time. A queued reply must not bypass the deadline.
        with patch("smoke_native.time.monotonic", side_effect=[0, 0, 31]):
            with self.assertRaisesRegex(RuntimeError, "metadata deadline exceeded"):
                client.response(lambda data: data.get("id") == 1)
        self.assertEqual(client.lines.qsize(), 1)

    def test_notifications_before_deadline_allow_matching_response(self):
        client = self.client_with_notifications()
        with patch("smoke_native.time.monotonic", side_effect=[0, 0, 1]):
            result = client.response(lambda data: data.get("id") == 1)
        self.assertEqual(result, {"id": 1, "result": {}})


if __name__ == "__main__":
    unittest.main()
