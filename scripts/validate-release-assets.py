#!/usr/bin/env python3
"""Validate the exact archives used by installer tests and publication."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import zipfile


TARGETS = {
    "darwin_amd64": ("tar.gz", "idiom"),
    "darwin_arm64": ("tar.gz", "idiom"),
    "linux_amd64": ("tar.gz", "idiom"),
    "linux_arm64": ("tar.gz", "idiom"),
    "windows_amd64": ("zip", "idiom.exe"),
}


def validate(dist: Path) -> None:
    checksums = {}
    for line in (dist / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([a-fA-F0-9]{64})  (\S+)", line)
        if not match:
            raise ValueError("invalid SHA256SUMS record")
        digest, name = match.groups()
        if name in checksums or Path(name).name != name or "\\" in name:
            raise ValueError("duplicate or unsafe checksum filename")
        checksums[name] = digest.lower()

    expected = set()
    versions = set()
    releases = dict(TARGETS, source=("tar.gz", None))
    for platform, (extension, binary) in releases.items():
        matches = list(dist.glob(f"idiom_*_{platform}.{extension}"))
        if len(matches) != 1:
            raise ValueError(f"expected exactly one archive for {platform}")
        archive = matches[0]
        expected.add(archive.name)
        versions.add(archive.name[len("idiom_") : -len(f"_{platform}.{extension}")])
        with archive.open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        if checksums.get(archive.name) != digest:
            raise ValueError(f"checksum mismatch: {archive.name}")

        if extension == "zip":
            with zipfile.ZipFile(archive) as source:
                entries = source.infolist()
                names = [entry.filename for entry in entries]
                regular = {entry.filename for entry in entries if not entry.is_dir()}
                notices = {name: source.read(name) for name in regular if name.startswith("third-party-licenses/")}
                if any((entry.external_attr >> 16) & 0o170000 == 0o120000 for entry in entries):
                    raise ValueError(f"symlink in {archive.name}")
        else:
            with tarfile.open(archive, "r:gz") as source:
                entries = source.getmembers()
                names = [entry.name for entry in entries]
                regular = {entry.name for entry in entries if entry.isfile()}
                notices = {name: source.extractfile(name).read() for name in regular if name.startswith("third-party-licenses/")}
                if any(not (entry.isfile() or entry.isdir()) for entry in entries):
                    raise ValueError(f"non-regular archive entry in {archive.name}")

        canonical = [name.rstrip("/") for name in names]
        identities = [name.casefold() if extension == "zip" else name for name in canonical]
        if len(identities) != len(set(identities)):
            raise ValueError(f"duplicate archive entries in {archive.name}")
        for name in canonical:
            path = PurePosixPath(name)
            if (path.is_absolute() or ".." in path.parts or "\\" in name
                    or str(path) != name or name == "."
                    or (extension == "zip" and (":" in name or any(
                        part.endswith((".", " ")) for part in path.parts)))):
                raise ValueError(f"unsafe archive path in {archive.name}")
        required = {"README.md", "LICENSE"}
        if binary:
            required.add(binary)
        else:
            required.update({"go.mod", "go.sum", "cmd/idiom/main.go", "vendor/modules.txt"})
        if not required.issubset(regular):
            raise ValueError(f"missing required source, binary or license files in {archive.name}")
        manifest_name = "third-party-licenses/manifest.json"
        if manifest_name not in notices:
            raise ValueError(f"missing dependency notices in {archive.name}")
        manifest = json.loads(notices.pop(manifest_name))
        inventory = {name: hashlib.sha256(data).hexdigest() for name, data in notices.items()}
        if manifest != inventory:
            raise ValueError(f"dependency notice inventory mismatch in {archive.name}")
        if "third-party-licenses/go/LICENSE" not in inventory or any(
            not any(name.startswith(f"third-party-licenses/{target}/") for name in inventory)
            for target in TARGETS
        ):
            raise ValueError(f"missing target dependency notices in {archive.name}")

    if len(versions) != 1:
        raise ValueError("archive versions differ")
    if expected != set(checksums):
        raise ValueError("checksum inventory differs from release archives")
    archives = {p.name for p in dist.iterdir() if p.name.endswith((".tar.gz", ".zip"))}
    if archives != expected:
        raise ValueError("unexpected release archive")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dist", type=Path, nargs="?", default=Path("dist"))
    args = parser.parse_args()
    try:
        validate(args.dist)
    except (OSError, ValueError, tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, f"release validation failed: {error}\n")
    print("Validated five binary archives, corresponding source, checksums, and license notices.")
