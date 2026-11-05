# Copyright (c) 2026 Neomantra Corp. All rights reserved.
# SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from release import checked_tag, homebrew_cask, release_notes, verify_archives


class ReleaseTests(unittest.TestCase):
    def test_tags_are_version_names(self):
        for tag in ("v0.1.0", "v1.2.3-rc.1"):
            self.assertEqual(checked_tag(tag), tag)
        for tag in ("main", "v01.2.3", "v1.2.3\n", "v1.2.3;echo bad", "--help", "../../main"):
            with self.assertRaises(ValueError, msg=tag):
                checked_tag(tag)

    def test_notes_use_only_the_tagged_release(self):
        changelog = "# CHANGELOG\n\n## Unreleased\n\n- Future\n\n## v0.1.0 (2026-11-05)\n\n- First release\n\n## v0.0.1 (2026-10-01)\n\n- Old\n"
        self.assertEqual(release_notes(changelog, "v0.1.0"), "- First release\n")
        for bad in ("## v0.1.0 (unreleased)\n- Draft", "## v0.1.0 (2026-11-05)\n\n## Other\n- Not this release", "## Unreleased\n- Future"):
            with self.assertRaises(ValueError):
                release_notes(bad, "v0.1.0")

    def packages(self, dist, missing=None, executable=True):
        paths = []
        files = ["ntbrowser", "ntbrowser-media", "README.md", "CHANGELOG.md", "LICENSE", "LICENSE-COMMERCIAL.md", "THIRD_PARTY_NOTICES.md", "licenses/ntbrowser-media-THIRD_PARTY_NOTICES.md"]
        for os in ("linux", "darwin"):
            for arch in ("amd64", "arm64"):
                path = dist / f"ntbrowser_0.1.0_{os}_{arch}.tar.gz"
                paths.append(path)
                with tarfile.open(path, "w:gz") as archive:
                    for name in files:
                        if name == missing:
                            continue
                        member = tarfile.TarInfo(name)
                        member.size = 7
                        member.mode = 0o755 if executable and name.startswith("ntbrowser") else 0o644
                        archive.addfile(member, io.BytesIO(b"fixture"))
        (dist / "checksums.txt").write_text("".join(f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n" for p in paths))
        return paths

    def test_complete_packages(self):
        with tempfile.TemporaryDirectory() as directory:
            dist = Path(directory)
            self.packages(dist)
            verify_archives(dist)

    def test_missing_helper_or_license_and_nonexecutable_binary(self):
        for missing, executable in (("ntbrowser-media", True), ("licenses/ntbrowser-media-THIRD_PARTY_NOTICES.md", True), (None, False)):
            with tempfile.TemporaryDirectory() as directory:
                dist = Path(directory)
                self.packages(dist, missing, executable)
                with self.assertRaises(ValueError):
                    verify_archives(dist)

    def test_checksum_and_target_coverage(self):
        for missing in (False, True):
            with tempfile.TemporaryDirectory() as directory:
                dist = Path(directory)
                paths = self.packages(dist)
                if missing:
                    paths[0].unlink()
                else:
                    with paths[0].open("ab") as archive:
                        archive.write(b"corruption")
                with self.assertRaises(ValueError):
                    verify_archives(dist)

    def test_cask_uses_exact_release_hashes_and_both_binaries(self):
        with tempfile.TemporaryDirectory() as directory:
            dist = Path(directory)
            archives = self.packages(dist)
            cask = homebrew_cask(dist, "v0.1.0")
            for archive in archives:
                if "_darwin_" in archive.name:
                    self.assertIn(hashlib.sha256(archive.read_bytes()).hexdigest(), cask)
            self.assertIn('binary "ntbrowser"', cask)
            self.assertIn('binary "ntbrowser-media"', cask)
            self.assertIn('version "0.1.0"', cask)
            with self.assertRaises(FileNotFoundError):
                homebrew_cask(dist, "v0.2.0")
