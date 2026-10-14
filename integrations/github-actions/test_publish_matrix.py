import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("publisher", pathlib.Path(__file__).with_name("publish_matrix.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class PublisherTests(unittest.TestCase):
    def report(self, verdict="fail"):
        return {"pairs": [{"pair": [1, 3], "members": [[1], [2, 3]], "verdict": verdict,
                           "failed_checks": [{"id": "migration_order"}] if verdict == "fail" else []}]}

    def test_requires_enablement_and_trust(self):
        for enabled, trusted in [(False, True), (True, False)]:
            with self.assertRaises(ValueError):
                publisher.publish("owner/repo", self.report(), "token", enabled, trusted)

    def test_create_update_and_unchanged_are_idempotent(self):
        rows = {}
        calls = []
        def api(path, token, method="GET", body=None):
            calls.append((method, path))
            if method == "GET":
                number = int(path.split("/issues/")[1].split("/")[0])
                return rows.get(number, [])
            if method == "POST":
                number = int(path.split("/issues/")[1].split("/")[0])
                rows[number] = [{"id": number + 100, "body": body["body"], "user": {"login": "github-actions[bot]"}}]
            else:
                number = int(path.rsplit("/", 1)[1]) - 100
                rows[number][0]["body"] = body["body"]
            return {}
        self.assertEqual(publisher.publish("owner/repo", self.report(), "token", True, True, api), ["created"] * 3)
        self.assertEqual(publisher.publish("owner/repo", self.report(), "token", True, True, api), ["unchanged"] * 3)
        self.assertEqual(publisher.publish("owner/repo", self.report("pass"), "token", True, True, api), ["updated"] * 3)
        self.assertEqual(sum(method == "POST" for method, _ in calls), 3)

    def test_attacker_marker_not_updated(self):
        calls = []
        def api(path, token, method="GET", body=None):
            calls.append(method)
            if method == "GET":
                return [{"id": 999, "user": {"login": "attacker"}, "body": "<!-- radar-pr-matrix:1-3 -->\nforged"}]
            return {}
        publisher.publish("owner/repo", self.report(), "token", True, True, api)
        self.assertNotIn("PATCH", calls)
        self.assertEqual(calls.count("POST"), 3)

    def test_new_passing_pair_does_not_spam(self):
        calls = []
        def api(path, token, method="GET", body=None):
            calls.append(method)
            return []
        self.assertEqual(publisher.publish("owner/repo", self.report("pass"), "token", True, True, api), ["skipped"] * 3)
        self.assertEqual(calls, ["GET"] * 3)

    def test_rejects_payload_injection(self):
        report = self.report()
        report["pairs"][0]["failed_checks"][0]["id"] = "@all\nforged"
        with self.assertRaises(ValueError):
            publisher.drafts(report)


if __name__ == "__main__":
    unittest.main()
