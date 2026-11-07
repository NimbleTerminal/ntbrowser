#!/usr/bin/env python3
# Copyright (c) 2026 Neomantra Corp. All rights reserved.
# SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
"""Release notes and archive checks, without publishing or executing artifacts."""

import hashlib
from pathlib import Path
import re
import sys
import tarfile


def checked_tag(tag):
    if not re.fullmatch(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", tag):
        raise ValueError("expected a version tag such as v0.1.0 or v0.2.0-rc.1")
    return tag


def release_notes(changelog, tag):
    checked_tag(tag)
    # A dated release entry is required; an Unreleased section cannot ship.
    heading = re.search(r"^## " + re.escape(tag) + r" \(\d{4}-\d{2}-\d{2}\)\s*$", changelog, re.M)
    if heading is None:
        raise ValueError(f"no dated changelog entry for {tag}")
    body = re.split(r"^## ", changelog[heading.end():], maxsplit=1, flags=re.M)[0].strip()
    if not body:
        raise ValueError(f"empty changelog entry for {tag}")
    return body + "\n"


def verify_archives(dist):
    expected_targets = {(os, arch) for os in ("linux", "darwin") for arch in ("amd64", "arm64")}
    required = {
        "ntbrowser", "glynx", "ntbrowser-media", "README.md", "CHANGELOG.md", "LICENSE",
        "LICENSE-COMMERCIAL.md", "THIRD_PARTY_NOTICES.md",
        "licenses/ntbrowser-media-THIRD_PARTY_NOTICES.md",
    }
    hashes = {}
    for line in (dist / "checksums.txt").read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        name = name.removeprefix("*")
        if Path(name).name != name or name in hashes or not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise ValueError("invalid checksum entry")
        hashes[name] = digest
    archives = list(dist.glob("*.tar.gz"))
    if set(hashes) != {p.name for p in archives}:
        raise ValueError("checksums must cover exactly the release archives")
    seen = set()
    for archive in archives:
        match = re.fullmatch(r"ntbrowser_.+_(linux|darwin)_(amd64|arm64)\.tar\.gz", archive.name)
        if match is None or match.groups() in seen:
            raise ValueError(f"unexpected or duplicate archive target: {archive.name}")
        seen.add(match.groups())
        with archive.open("rb") as stream:
            if hashlib.file_digest(stream, "sha256").hexdigest() != hashes[archive.name]:
                raise ValueError(f"checksum mismatch: {archive.name}")
        with tarfile.open(archive) as package:
            members = package.getmembers()
            files = {m.name: m for m in members if m.isfile()}
            if len(files) != len(members) or set(files) != required:
                raise ValueError(f"wrong package contents: {archive.name}: {sorted(files)}")
            if any(m.size == 0 for m in members):
                raise ValueError(f"empty package member: {archive.name}")
            for binary in ("ntbrowser", "glynx", "ntbrowser-media"):
                if not files[binary].mode & 0o111:
                    raise ValueError(f"non-executable {binary}: {archive.name}")
    if seen != expected_targets:
        raise ValueError(f"missing archive targets: {expected_targets - seen}")


def homebrew_cask(dist, tag):
    checked_tag(tag)
    verify_archives(dist)
    version = tag[1:]
    digests = {}
    for arch in ("arm64", "amd64"):
        archive = dist / f"ntbrowser_{version}_darwin_{arch}.tar.gz"
        with archive.open("rb") as stream:
            digests[arch] = hashlib.file_digest(stream, "sha256").hexdigest()
    return f'''cask "ntbrowser" do
  arch arm: "arm64", intel: "amd64"

  version "{version}"
  sha256 arm:   "{digests['arm64']}",
         intel: "{digests['amd64']}"

  url "https://github.com/NimbleTerminal/ntbrowser/releases/download/v#{{version}}/ntbrowser_#{{version}}_darwin_#{{arch}}.tar.gz"
  name "ntbrowser"
  desc "Terminal web browser with confined media playback"
  homepage "https://github.com/NimbleTerminal/ntbrowser"

  binary "ntbrowser"
  binary "glynx"
  binary "ntbrowser-media"

  caveats <<~EOS
    For video playback, install libmpv with: brew install mpv
    Keep ntbrowser and ntbrowser-media installed together.
  EOS
end
'''


if __name__ == "__main__":
    try:
        if len(sys.argv) == 3 and sys.argv[1] == "tag":
            checked_tag(sys.argv[2])
        elif len(sys.argv) == 4 and sys.argv[1] == "notes":
            print(release_notes(Path(sys.argv[2]).read_text(), sys.argv[3]), end="")
        elif len(sys.argv) == 3 and sys.argv[1] == "verify":
            verify_archives(Path(sys.argv[2]))
        elif len(sys.argv) == 4 and sys.argv[1] == "cask":
            print(homebrew_cask(Path(sys.argv[2]), sys.argv[3]), end="")
        else:
            raise ValueError("usage: release.py tag TAG | notes CHANGELOG TAG | verify DIST | cask DIST TAG")
    except (ValueError, OSError, tarfile.TarError) as error:
        sys.exit(f"release: {error}")
