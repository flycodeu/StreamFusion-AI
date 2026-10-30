#!/usr/bin/env python3
"""Offline tests for release sidecar staging; no package is executed."""

import hashlib
import importlib.util
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("material_stage", Path(__file__).with_name("stage-material-library.py"))
stage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stage)


class MaterialReleaseStageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.library, self.output = self.root / "input", self.root / "release"
        self.library.mkdir()
        self.output.mkdir()
        self.material = {"recipeKey": "java", "recipeVersion": "8", "recipeRevision": "java-8-v1", "runtimeMode": "NATIVE", "name": "Test only", "file": "jdk package.tar.gz", "sha256": hashlib.sha256(b"fixture bytes").hexdigest(), "byteSize": len(b"fixture bytes")}
        (self.library / self.material["file"]).write_bytes(b"fixture bytes")
        self.manifest()

    def manifest(self):
        (self.library / "library.json").write_text(json.dumps({"schemaVersion": 1, "materials": [self.material]}), encoding="utf-8")

    def test_only_declared_files_are_staged_and_tarred(self):
        (self.library / "ignore.tar").write_bytes(b"not listed")
        stale = self.output / "materials"
        stale.mkdir()
        (stale / "stale.tar").write_bytes(b"preserve on disk")
        result = stage.stage(self.library, self.output)
        self.assertEqual(result["files"], ["materials/library.json", "materials/jdk package.tar.gz"])
        self.assertEqual((stale / "jdk package.tar.gz").read_bytes(), b"fixture bytes")
        self.assertTrue((stale / "stale.tar").exists())
        checksums = "".join(hashlib.sha256((self.output / name).read_bytes()).hexdigest() + "  " + name + "\n" for name in result["files"])
        self.assertIn("materials/library.json", checksums)
        with tarfile.open(self.root / "release.tar", "w") as archive:
            for name in result["files"]:
                archive.add(self.output / name, arcname=name, recursive=False)
        with tarfile.open(self.root / "release.tar") as archive:
            self.assertEqual(archive.getnames(), result["files"])

    def test_same_source_destination_revalidates_without_deleting(self):
        stage.stage(self.library, self.output)
        result = stage.stage(self.output / "materials", self.output)
        self.assertEqual(result["materials"], 1)

    def test_missing_default_skips_and_explicit_missing_fails(self):
        self.assertEqual(stage.stage(self.root / "missing", self.output, True)["files"], [])
        (self.library / "library.json").unlink()
        self.assertEqual(stage.stage(self.library, self.output, True)["files"], [])
        with self.assertRaises((ValueError, OSError)):
            stage.stage(self.library, self.output)

    def test_hash_size_schema_and_traversal_rejected(self):
        for key, bad in [("sha256", "0" * 64), ("byteSize", 999), ("file", "../escape"), ("file", "library.json"), ("file", "LIBRARY.JSON"), ("file", "library.json "), ("file", "x\nfile")]:
            old = self.material[key]
            self.material[key] = bad
            self.manifest()
            with self.subTest(key=key, bad=bad), self.assertRaises((ValueError, OSError)):
                stage.stage(self.library, self.output)
            self.material[key] = old
        (self.library / "library.json").write_text('{"schemaVersion":1,"schemaVersion":1,"materials":[]}', encoding="utf-8")
        with self.assertRaises(ValueError):
            stage.stage(self.library, self.output)

    def test_symlink_source_and_library_rejected(self):
        original = self.library / self.material["file"]
        external = self.root / "external"
        external.write_bytes(original.read_bytes())
        original.unlink()
        try:
            original.symlink_to(external)
        except OSError as exc:
            self.skipTest(f"symlink creation unavailable: {exc}")
        with self.assertRaises(ValueError):
            stage.stage(self.library, self.output)
        alias = self.root / "alias"
        alias.symlink_to(self.library, target_is_directory=True)
        with self.assertRaises(ValueError):
            stage.stage(alias, self.output, True)


if __name__ == "__main__":
    unittest.main()
