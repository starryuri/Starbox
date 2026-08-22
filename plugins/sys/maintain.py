#!/usr/bin/env python3
"""System maintenance / info plugin. SAFE by default: it only REPORTS.

Read-only: prints top processes by memory and TEMP folder usage. It does NOT
delete anything. To actually clean, add explicit logic yourself.

Only stdlib is used; short-lived subprocess (no resident memory).
"""
import os
import subprocess
import sys


def top_processes(n=5):
    try:
        out = subprocess.run(
            ["tasklist", "/fo", "csv", "/nh"], capture_output=True, text=True, timeout=15
        ).stdout or ""
        rows = []
        for line in out.splitlines():
            parts = line.split('","')
            if len(parts) < 5:
                continue
            name = parts[0].strip('"')
            mem = parts[4].strip('"').replace(",", "").replace(" K", "")
            try:
                kb = int(float(mem))
            except ValueError:
                continue
            rows.append((kb, name))
        rows.sort(reverse=True)
        return rows[:n]
    except Exception as e:  # noqa: BLE001
        return [("tasklist failed", 0)]


def dir_size(path):
    total = 0
    count = 0
    for root, _dirs, files in os.walk(path):
        for f in files:
            try:
                total += os.path.getsize(os.path.join(root, f))
                count += 1
            except OSError:
                pass
    return total, count


def human(n):
    for u in ["B", "KB", "MB", "GB", "TB"]:
        if n < 1024:
            return f"{n:.1f}{u}"
        n /= 1024
    return f"{n:.1f}TB"


def main():
    print("== top processes by memory ==")
    for kb, name in top_processes():
        print(f"{name}: {kb / 1024:.0f} MB")
    temp = os.environ.get("TEMP", os.environ.get("TMP", r"C:\Windows\Temp"))
    size, count = dir_size(temp)
    print("== temp report (read-only, nothing deleted) ==")
    print(f"temp={temp}")
    print(f"temp_size={human(size)} files={count}")
    print("system report ok")


if __name__ == "__main__":
    sys.exit(main())
