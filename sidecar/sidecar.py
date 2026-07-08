#!/usr/bin/env python3
import json, os, subprocess, sys
from curl_cffi import requests

def load_cookies(bridge, args=None):
    if not bridge:
        sys.stderr.write("DEBUG: no bridge\n")
        return None
    try:
        cmd = ["node", bridge]
        if args:
            cmd.extend(args)
        sys.stderr.write(f"DEBUG: cmd={cmd}\n")
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=15, check=True).stdout.strip()
        sys.stderr.write(f"DEBUG: cookies={len(out)} chars\n")
        return out if out else None
    except Exception as e:
        sys.stderr.write(f"DEBUG: error={e}\n")
        return None

def main():
    req = json.loads(sys.stdin.read())
    headers = dict(req.get("headers", {}))
    bridge = req.get("cookie_bridge") or os.environ.get("AGENTHAIL_COOKIE_BRIDGE")
    cookie = load_cookies(bridge, req.get("cookie_bridge_args"))
    if cookie:
        headers["cookie"] = cookie
    else:
        sys.stderr.write("DEBUG: NO COOKIES\n")
    sys.stderr.write(f"DEBUG: url={req.get('url','?')[:60]}\n")
    sys.stderr.write(f"DEBUG: has_cookie={'cookie' in headers}\n")
    try:
        resp = requests.request(
            method=req.get("method", "POST"), url=req["url"], headers=headers,
            data=req.get("body", "").encode("utf-8") if req.get("body") else None,
            impersonate="chrome", timeout=req.get("timeout", 30))
        out = {"status": resp.status_code, "body": resp.text, "error": ""}
    except Exception as e:
        out = {"status": 0, "body": "", "error": str(e)}
    sys.stdout.write(json.dumps(out))
    sys.stdout.flush()

if __name__ == "__main__":
    main()
