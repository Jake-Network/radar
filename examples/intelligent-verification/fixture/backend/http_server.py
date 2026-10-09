import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from .service import product_response


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path != "/products/demo-product":
            self.send_error(404)
            return
        content = json.dumps(product_response()).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(content)))
        self.end_headers()
        self.wfile.write(content)

    def log_message(self, *args):
        pass


def server():
    return ThreadingHTTPServer(("127.0.0.1", 0), Handler)
