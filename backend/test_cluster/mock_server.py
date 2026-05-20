import http.server
import socketserver
import json
import urllib.parse
import os

PORT = 9999
alerts_file = "alerts.log"

# Clear alerts log
with open(alerts_file, "w") as f:
    pass

states = {
    "db-primary": 200,
    "api-gateway": 200,
    "checkout": 200,
    "standalone-target": 200
}

class MockHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        parsed_path = urllib.parse.urlparse(self.path)
        path = parsed_path.path.strip("/")
        
        if path in states:
            status = states[path]
            self.send_response(status)
            self.end_headers()
            self.wfile.write(b"OK" if status == 200 else b"ERROR")
        else:
            self.send_response(404)
            self.end_headers()

    def do_POST(self):
        parsed_path = urllib.parse.urlparse(self.path)
        path = parsed_path.path.strip("/")
        
        if path == "control":
            content_len = int(self.headers.get('content-length', 0))
            post_body = self.rfile.read(content_len)
            data = json.loads(post_body)
            target = data.get("target")
            status = data.get("status")
            if target in states:
                states[target] = status
                self.send_response(200)
            else:
                self.send_response(404)
            self.end_headers()
        elif path == "webhook":
            content_len = int(self.headers.get('content-length', 0))
            post_body = self.rfile.read(content_len)
            alert = json.loads(post_body)
            with open(alerts_file, "a") as f:
                f.write(json.dumps(alert) + "\n")
            self.send_response(200)
            self.end_headers()
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        pass # Suppress logging

with socketserver.TCPServer(("127.0.0.1", PORT), MockHandler) as httpd:
    print(f"Mock server running on port {PORT}")
    httpd.serve_forever()
