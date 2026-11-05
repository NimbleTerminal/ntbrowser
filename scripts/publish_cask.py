#!/usr/bin/env python3
# Copyright (c) 2026 Neomantra Corp. All rights reserved.
# SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
"""Publish a generated cask using the tap-scoped GH_TOKEN; never force-push."""
import base64
import json
from pathlib import Path
import subprocess
import sys

from release import checked_tag


def publish(path, tag):
    checked_tag(tag)
    if "-" in tag:
        raise ValueError("prereleases must not update the stable tap")
    endpoint = "repos/NimbleTerminal/homebrew-tap/contents/Casks/ntbrowser.rb"
    # Listing the directory distinguishes a missing cask from permission/network
    # errors. The tap is expected to already contain its Casks directory.
    listing = json.loads(subprocess.check_output(
        ["gh", "api", "repos/NimbleTerminal/homebrew-tap/contents/Casks?ref=main"]))
    existing = next((entry for entry in listing if entry["name"] == "ntbrowser.rb"), None)
    content = path.read_bytes()
    if existing:
        current = json.loads(subprocess.check_output(["gh", "api", endpoint + "?ref=main"]))
        if base64.b64decode(current["content"]) == content:
            return
    payload = {
        "message": f"chore: update ntbrowser to {tag}",
        "content": base64.b64encode(content).decode(),
        "branch": "main",
    }
    if existing:
        payload["sha"] = existing["sha"]
    subprocess.run(["gh", "api", "--method", "PUT", endpoint, "--input", "-"],
                   input=json.dumps(payload), text=True, check=True, stdout=subprocess.DEVNULL)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: publish_cask.py CASK TAG")
    publish(Path(sys.argv[1]), sys.argv[2])
