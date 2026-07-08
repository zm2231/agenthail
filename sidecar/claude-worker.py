#!/usr/bin/env python3
"""agenthail Claude HTTP sidecar. Reads a JSON request on stdin, performs the
request with curl_cffi Chrome impersonation, writes a JSON response on stdout.
Loads cookies via the node bridge when AGENTHAIL_COOKIE_BRIDGE is set (more
reliable than Go's sweetcookie, which can return stale CF cookies)."""

import json
import os
import subprocess
import sys
from curl_cffi import requests


def load_cookies(profile):
    bridge = os.environ.get("AGENTHAIL_COOKIE_BRIDGE")
    if not bridge:
        return None
    try:
        out = subprocess.run(
            ["node", bridge, profile],
            capture_output=True,
            text=True,
            timeout=15,
            check=True,
        ).stdout.strip()
        if "sessionKey=" in out:
            return out
    except Exception:
        pass
    return None


def main():
    req = json.loads(sys.stdin.read())
    headers = dict(req.get("headers", {}))
    cookie = load_cookies(req.get("profile", "Default"))
    if cookie:
        headers["cookie"] = cookie
    try:
        resp = requests.request(
            method=req.get("method", "POST"),
            url=req["url"],
            headers=headers,
            data=req.get("body", "").encode("utf-8") if req.get("body") else None,
            impersonate="chrome",
            timeout=req.get("timeout", 30),
        )
        out = {"status": resp.status_code, "body": resp.text, "error": ""}
    except Exception as e:
        out = {"status": 0, "body": "", "error": str(e)}
    sys.stdout.write(json.dumps(out))
    sys.stdout.flush()


if __name__ == "__main__":
    main()
