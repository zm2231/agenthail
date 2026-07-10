#!/usr/bin/env python3
import json, os, subprocess, sys
from curl_cffi import requests

DEBUG = os.environ.get("AGENTHAIL_DEBUG") == "1"

def debug(message):
    if DEBUG:
        sys.stderr.write(f"DEBUG: {message}\n")

def load_cookies(bridge, args=None, profile=None):
    if not bridge:
        debug("no cookie bridge")
        return None
    try:
        cmd = ["node", bridge]
        if args:
            cmd.extend(args)
        env = os.environ.copy()
        if profile:
            env["AGENTHAIL_CHROME_PROFILE"] = profile
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=15, check=True, env=env).stdout.strip()
        debug(f"cookie header length={len(out)}")
        return out if out else None
    except Exception as e:
        raise RuntimeError(f"cookie bridge failed: {type(e).__name__}: {e}") from e

def main():
    try:
        req = json.loads(sys.stdin.read())
    except Exception as e:
        sys.stdout.write(json.dumps({"status": 0, "body": "", "error": f"invalid request JSON: {e}"}))
        return
    if not isinstance(req, dict) or not isinstance(req.get("url"), str):
        sys.stdout.write(json.dumps({"status": 0, "body": "", "error": "request requires a URL"}))
        return
    headers = dict(req.get("headers", {}))
    bridge = req.get("cookie_bridge") or os.environ.get("AGENTHAIL_COOKIE_BRIDGE")
    try:
        cookie = load_cookies(bridge, req.get("cookie_bridge_args"), req.get("profile"))
    except Exception as e:
        sys.stdout.write(json.dumps({"status": 0, "body": "", "error": str(e)}))
        return
    if cookie:
        headers["cookie"] = cookie
    debug(f"request host={req['url'].split('/')[2] if '//' in req['url'] else '?'} has_cookie={'cookie' in headers}")
    try:
        resp = requests.request(
            method=req.get("method", "POST"), url=req["url"], headers=headers,
            data=req.get("body", "").encode("utf-8") if req.get("body") else None,
            impersonate="chrome", timeout=req.get("timeout", 30), stream=True)
        max_bytes = int(os.environ.get("AGENTHAIL_MAX_RESPONSE_BYTES", str(16 * 1024 * 1024)))
        chunks = []
        received = 0
        for chunk in resp.iter_content(chunk_size=64 * 1024):
            if not chunk:
                continue
            received += len(chunk)
            if received > max_bytes:
                resp.close()
                raise ValueError(f"response exceeded {max_bytes} bytes")
            chunks.append(chunk)
        encoding = resp.encoding or "utf-8"
        body = b"".join(chunks).decode(encoding, errors="replace")
        out = {"status": resp.status_code, "body": body, "error": ""}
    except Exception as e:
        out = {"status": 0, "body": "", "error": str(e)}
    sys.stdout.write(json.dumps(out))
    sys.stdout.flush()

if __name__ == "__main__":
    main()
