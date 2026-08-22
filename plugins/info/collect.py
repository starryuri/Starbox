#!/usr/bin/env python3
"""Info-collection template.

This is your hook for 'collect what I want to know'. Point it at any API or
page and print the facts you care about as text. Runs as a subprocess, so it
never leaves anything resident. Customize freely.

Example: IMAP_HOST-style env overrides or hardcoded URLs. Below pulls a public
JSON API and prints a couple of fields.
"""
import json
import os
import sys
import urllib.request


def fetch_json(url):
    req = urllib.request.Request(url, headers={"User-Agent": "butler/0.1"})
    with urllib.request.urlopen(req, timeout=20) as resp:
        return json.loads(resp.read().decode("utf-8"))


def main():
    # Customize: list the URLs/facts you care about.
    urls = [
        # e.g. "https://api.github.com/zen",
    ]
    for u in urls:
        try:
            data = fetch_json(u)
            print(u, "->", data)
        except Exception as e:  # noqa: BLE001
            print(u, "-> ERROR", e)

    # If you want to trigger a rule, print something like: ALERT <msg>
    # and the core will show it in the task snapshot + HTTP endpoint.
    print("info collection ran")


if __name__ == "__main__":
    sys.exit(main())
