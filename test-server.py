#!/usr/bin/env python3
"""Simple HTTP server that mimics reasonix /status and settings endpoints."""
import json
from http.server import HTTPServer, BaseHTTPRequestHandler

settings = {"mode": "ask"}

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/status":
            data = {"cwd": "/test/sessions", "label": "test-model", "toolApprovalMode": settings["mode"]}
        else:
            data = {"error": "not found"}
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length) if length else b""
        if self.path == "/tool-approval-mode":
            try:
                j = json.loads(body)
                settings["mode"] = j.get("mode", settings["mode"])
            except: pass
        elif self.path == "/submit":
            try:
                j = json.loads(body)
                print(f"  submit: {j.get('input','')}", flush=True)
            except: pass
        self.send_response(204)
        self.end_headers()

    def log_message(self, *a): pass

HTTPServer(("127.0.0.1", 8788), Handler).serve_forever()
