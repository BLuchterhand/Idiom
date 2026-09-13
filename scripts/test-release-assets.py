#!/usr/bin/env python3
"""Regression checks for release archive acceptance and rejection."""

import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location(
    "release_assets", Path(__file__).with_name("validate-release-assets.py")
)
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class ReleaseAssetsTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.dist = Path(self.directory.name)
        self.write_archives()

    def write_archives(self, omit=None, extra=None):
        for platform, (extension, binary) in dict(assets.TARGETS, source=("tar.gz", None)).items():
            archive = self.dist / f"idiom_0.1.0_{platform}.{extension}"
            entries = {"README.md": b"guide", "LICENSE": b"license",
                       "third-party-licenses/go/LICENSE": b"Go license"}
            if binary:
                entries[binary] = b"binary"
            else:
                entries.update({"go.mod": b"module example", "go.sum": b"sum",
                                "cmd/idiom/main.go": b"package main", "vendor/modules.txt": b"modules"})
            for target in assets.TARGETS:
                entries[f"third-party-licenses/{target}/example/LICENSE"] = b"notice"
            entries["third-party-licenses/manifest.json"] = json.dumps({
                name: hashlib.sha256(data).hexdigest() for name, data in entries.items()
                if name.startswith("third-party-licenses/")
            }).encode()
            entries.pop(omit, None)
            if extra:
                entries.update(extra)
            if extension == "zip":
                with zipfile.ZipFile(archive, "w") as output:
                    for name, data in entries.items():
                        output.writestr(name, data)
            else:
                with tarfile.open(archive, "w:gz") as output:
                    for name, data in entries.items():
                        entry = tarfile.TarInfo(name)
                        entry.size = len(data)
                        output.addfile(entry, io.BytesIO(data))
        self.write_checksums()

    def write_checksums(self):
        lines = []
        for archive in sorted(self.dist.glob("idiom_*")):
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            lines.append(f"{digest}  {archive.name}\n")
        (self.dist / "SHA256SUMS").write_text("".join(lines))

    def test_accepts_complete_release(self):
        assets.validate(self.dist)

    def test_rejects_corrupt_archive(self):
        archive = next(self.dist.glob("*.zip"))
        archive.write_bytes(archive.read_bytes() + b"tampered")
        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
            assets.validate(self.dist)

    def test_requires_binary_and_notices(self):
        for name in ("idiom", "LICENSE", "third-party-licenses/manifest.json"):
            with self.subTest(name=name):
                self.write_archives(omit=name)
                with self.assertRaisesRegex(ValueError, "missing"):
                    assets.validate(self.dist)

    def test_rejects_incomplete_notice_inventory(self):
        self.write_archives(omit="third-party-licenses/go/LICENSE")
        with self.assertRaisesRegex(ValueError, "notice inventory mismatch"):
            assets.validate(self.dist)

    def test_rejects_missing_target(self):
        next(self.dist.glob("*linux_arm64.tar.gz")).unlink()
        with self.assertRaisesRegex(ValueError, "exactly one"):
            assets.validate(self.dist)

    def test_requires_corresponding_source(self):
        next(self.dist.glob("*_source.tar.gz")).unlink()
        with self.assertRaisesRegex(ValueError, "exactly one"):
            assets.validate(self.dist)

    def test_requires_vendored_source(self):
        self.write_archives(omit="vendor/modules.txt")
        with self.assertRaisesRegex(ValueError, "missing required source"):
            assets.validate(self.dist)

    def test_rejects_traversal(self):
        self.write_archives(extra={"../outside": b"bad"})
        with self.assertRaisesRegex(ValueError, "unsafe archive path"):
            assets.validate(self.dist)

    def test_rejects_extraction_aliases(self):
        for name in ("./idiom", "folder//file", "C:relative", "README.md:stream", "idiom.exe.", "folder /file"):
            with self.subTest(name=name):
                self.write_archives(extra={name: b"bad"})
                with self.assertRaisesRegex(ValueError, "unsafe archive path"):
                    assets.validate(self.dist)

    def test_rejects_windows_case_aliases(self):
        self.write_archives(extra={"readme.md": b"bad"})
        with self.assertRaisesRegex(ValueError, "duplicate archive"):
            assets.validate(self.dist)

    def test_rejects_duplicate_checksum(self):
        path = self.dist / "SHA256SUMS"
        path.write_text(path.read_text() * 2)
        with self.assertRaisesRegex(ValueError, "duplicate"):
            assets.validate(self.dist)

    def test_rejects_mixed_versions(self):
        archive = next(self.dist.glob("*.zip"))
        archive.rename(archive.with_name(archive.name.replace("0.1.0", "0.2.0")))
        self.write_checksums()
        with self.assertRaisesRegex(ValueError, "versions differ"):
            assets.validate(self.dist)

    def test_rejects_symlink(self):
        archive = next(self.dist.glob("*linux_arm64.tar.gz"))
        with tarfile.open(archive, "w:gz") as output:
            entry = tarfile.TarInfo("idiom")
            entry.type = tarfile.SYMTYPE
            entry.linkname = "/bin/sh"
            output.addfile(entry)
        self.write_checksums()
        with self.assertRaisesRegex(ValueError, "non-regular"):
            assets.validate(self.dist)


if __name__ == "__main__":
    unittest.main()
