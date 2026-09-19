#!/usr/bin/env python3
# Copyright 2026 The mintfog Authors
# SPDX-License-Identifier: Apache-2.0

import importlib.util
import io
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

spec = importlib.util.spec_from_file_location(
    "coverage_gate", Path(__file__).resolve().parents[1] / "check-update-coverage.py"
)
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class CoverageGateTest(unittest.TestCase):
    def parse(self, contents):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "coverage.out"
            path.write_text(contents, encoding="utf-8")
            return gate.read_profile(path)

    def check(self, blocks):
        with redirect_stdout(io.StringIO()):
            return gate.check(blocks)

    def complete(self):
        return {
            (scope + "update.go" if scope.endswith("/") else scope, "1.1,2.1"): (100, True)
            for scope in gate.THRESHOLDS
        }

    def test_merge_duplicate_blocks_from_multiple_packages(self):
        file = gate.MODULE + "internal/update/update.go"
        blocks = self.parse(f"mode: atomic\n{file}:1.1,2.1 10 0\n{file}:1.1,2.1 10 3\n{file}:1.1,2.1 10 0\n")
        self.assertEqual(blocks, {("internal/update/update.go", "1.1,2.1"): (10, True)})

    def test_reject_invalid_or_conflicting_profile(self):
        for contents in ("", "mode: unknown\n", "mode: atomic\nbroken\n", "mode: atomic\nx:1.1,2.1 1 0\nx:1.1,2.1 2 1\n"):
            with self.subTest(contents=contents), self.assertRaises(ValueError):
                self.parse(contents)

    def test_require_every_scope(self):
        blocks = self.complete()
        self.assertTrue(self.check(blocks))
        blocks.pop(next(iter(blocks)))
        self.assertFalse(self.check(blocks))
        self.assertFalse(self.check(self.parse("mode: atomic\n")))

    def test_unrelated_coverage_cannot_mask_a_failure(self):
        blocks = self.complete()
        blocks[("internal/update/download.go", "3.1,4.1")] = (20, False)
        blocks[("internal/core/engine.go", "1.1,2.1")] = (10000, True)
        self.assertFalse(self.check(blocks))


if __name__ == "__main__":
    unittest.main()
