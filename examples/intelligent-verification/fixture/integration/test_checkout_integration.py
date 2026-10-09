import subprocess
import threading
import unittest
from backend.http_server import server


class CheckoutIntegration(unittest.TestCase):
    def test_client_total_stays_within_approved_budget(self):
        http = server()
        worker = threading.Thread(target=http.serve_forever, daemon=True)
        worker.start()
        try:
            result = subprocess.run(
                ["node", "frontend/client.mjs", f"http://127.0.0.1:{http.server_port}"],
                capture_output=True, text=True, timeout=10, check=True,
            )
            self.assertLessEqual(int(result.stdout.strip()), 2)
        finally:
            http.shutdown()
            http.server_close()
            worker.join(timeout=2)
