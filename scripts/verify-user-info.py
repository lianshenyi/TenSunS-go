#!/usr/bin/env python3
"""Verify the authenticated user-info visual-identity contract without logging secrets."""

import getpass
import json
import sys
import urllib.error
import urllib.request


def request(base, path, body=None, token=None):
    headers = {"Content-Type": "application/json"} if body is not None else {}
    if token:
        headers["Authorization"] = token
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.loads(response.read())


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: verify-user-info.py http://host:port")

    base = sys.argv[1].rstrip("/")
    password = getpass.getpass("Authorized admin password: ")
    try:
        login = request(base, "/api/user/login", {"username": "admin", "password": password})
        if login.get("code") != 20000:
            raise RuntimeError("login was rejected")
        info = request(base, "/api/user/info", token=login["data"]["token"])
    except (KeyError, RuntimeError, urllib.error.URLError, urllib.error.HTTPError) as error:
        raise SystemExit("FAIL: authenticated user-info contract could not be verified") from error

    expected = {"roles": ["admin"], "name": "admin", "avatar": "/harbor-mark.svg"}
    if info.get("code") != 20000 or info.get("data") != expected:
        raise SystemExit("FAIL: authenticated user-info contract differs from the expected harbor mark")
    print("PASS: authenticated /api/user/info returns the harbor-mark avatar contract")


if __name__ == "__main__":
    main()
