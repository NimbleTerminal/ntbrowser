# Copyright (c) 2026 Neomantra Corp. All rights reserved.
# SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
import base64
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from publish_cask import publish


class PublishTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "ntbrowser.rb"
        self.path.write_text("cask fixture")

    @patch("publish_cask.subprocess.run")
    @patch("publish_cask.subprocess.check_output")
    def test_create_or_update_uses_compare_and_swap(self, read, write):
        for existing in (False, True):
            with self.subTest(existing=existing):
                read.side_effect = (
                    [json.dumps([{"name": "ntbrowser.rb", "sha": "old-sha"}]),
                     json.dumps({"content": base64.b64encode(b"old cask").decode()})]
                    if existing else ["[]"])
                publish(self.path, "v0.1.0")
                payload = json.loads(write.call_args.kwargs["input"])
                self.assertEqual(payload.get("sha"), "old-sha" if existing else None)
                self.assertEqual(base64.b64decode(payload["content"]), self.path.read_bytes())
                self.assertEqual(payload["branch"], "main")

    @patch("publish_cask.subprocess.run")
    @patch("publish_cask.subprocess.check_output")
    def test_read_failure_never_writes(self, read, write):
        read.side_effect = subprocess.CalledProcessError(1, "gh")
        with self.assertRaises(subprocess.CalledProcessError):
            publish(self.path, "v0.1.0")
        write.assert_not_called()

    @patch("publish_cask.subprocess.run")
    @patch("publish_cask.subprocess.check_output")
    def test_identical_retry_and_prerelease_never_write(self, read, write):
        read.side_effect = [
            json.dumps([{"name": "ntbrowser.rb", "sha": "existing"}]),
            json.dumps({"content": base64.b64encode(self.path.read_bytes()).decode()})]
        publish(self.path, "v0.1.0")
        with self.assertRaises(ValueError):
            publish(self.path, "v0.2.0-rc.1")
        write.assert_not_called()
