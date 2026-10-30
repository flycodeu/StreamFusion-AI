#!/usr/bin/env python3
"""Validate and stage an optional release sidecar without network or execution.

Only library.json and its declared regular files enter the release. Existing
unlisted release files are preserved on disk and excluded from the tar manifest.
Recipe/content trust is still checked by the controller's compiled importer.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import tempfile
import time

ENTRY_FIELDS = {"recipeKey", "recipeVersion", "recipeRevision", "runtimeMode", "name", "file", "sha256", "byteSize"}
ARCHIVE_LIMIT = 2 << 30
LIBRARY_LIMIT = 16 << 30


def protected_path(path: Path, directory: bool) -> Path:
    path = Path(os.path.abspath(path))
    for component in [path, *path.parents]:
        info = component.lstat()
        if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0):
            raise ValueError("MATERIAL_STAGE_SYMLINK_FORBIDDEN")
        if component == path:
            if directory and not stat.S_ISDIR(info.st_mode) or not directory and not stat.S_ISREG(info.st_mode):
                raise ValueError("MATERIAL_STAGE_PATH_INVALID")
        elif not stat.S_ISDIR(info.st_mode):
            raise ValueError("MATERIAL_STAGE_PATH_INVALID")
    return path


def strict_object(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("MATERIAL_STAGE_DUPLICATE_JSON_KEY")
        result[key] = value
    return result


def valid_name(name: object) -> bool:
    return isinstance(name, str) and name not in {"", ".", ".."} and name.casefold() != "library.json" and name.rstrip(" .") == name and len(name) <= 240 and not any(char in name for char in '/\\:<>"|?*') and not any(ord(char) < 32 for char in name) and Path(name).name == name


def copy_checked(source: Path, size: int, digest: str, destination: Path | None, deadline: float) -> None:
    source = protected_path(source, False)
    before = source.lstat()
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
    temporary = None
    try:
        with os.fdopen(os.open(source, flags), "rb") as reader:
            opened = os.fstat(reader.fileno())
            if not os.path.samestat(before, opened) or opened.st_size != size:
                raise ValueError("MATERIAL_STAGE_SOURCE_CHANGED")
            if destination is not None:
                if destination.exists() or destination.is_symlink():
                    protected_path(destination, False)
                descriptor, name = tempfile.mkstemp(prefix=".material-stage-", dir=destination.parent)
                temporary = Path(name)
                writer = os.fdopen(descriptor, "wb")
            else:
                writer = None
            try:
                checksum, copied = hashlib.sha256(), 0
                while block := reader.read(1 << 20):
                    copied += len(block)
                    if copied > size or time.monotonic() > deadline:
                        raise ValueError("MATERIAL_STAGE_SIZE_OR_DEADLINE")
                    checksum.update(block)
                    if writer is not None:
                        writer.write(block)
                if copied != size or checksum.hexdigest() != digest:
                    raise ValueError("MATERIAL_STAGE_HASH_OR_SIZE_MISMATCH")
                after = os.fstat(reader.fileno())
                if (after.st_size, after.st_mtime_ns) != (opened.st_size, opened.st_mtime_ns):
                    raise ValueError("MATERIAL_STAGE_SOURCE_CHANGED")
                if writer is not None:
                    writer.flush()
                    os.fsync(writer.fileno())
            finally:
                if writer is not None:
                    writer.close()
        if destination is not None:
            os.replace(temporary, destination)
            temporary = None
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def stage(library: Path, output: Path, optional: bool = False) -> dict:
    if optional and not library.exists() and not library.is_symlink():
        return {"files": [], "materials": 0, "bytes": 0}
    library = protected_path(library, True)
    manifest = library / "library.json"
    if optional and not manifest.exists() and not manifest.is_symlink():
        return {"files": [], "materials": 0, "bytes": 0}
    manifest = protected_path(library / "library.json", False)
    if not 0 < manifest.stat().st_size <= 1 << 20:
        raise ValueError("MATERIAL_STAGE_MANIFEST_LIMIT")
    with manifest.open("rb") as stream:
        raw = stream.read((1 << 20) + 1)
    if len(raw) > 1 << 20:
        raise ValueError("MATERIAL_STAGE_MANIFEST_LIMIT")
    payload = json.loads(raw, object_pairs_hook=strict_object)
    if not isinstance(payload, dict) or set(payload) != {"schemaVersion", "materials"} or type(payload["schemaVersion"]) is not int or payload["schemaVersion"] != 1 or not isinstance(payload["materials"], list) or len(payload["materials"]) > 100:
        raise ValueError("MATERIAL_STAGE_MANIFEST_INVALID")
    entries, names, total = payload["materials"], set(), 0
    for item in entries:
        if not isinstance(item, dict) or set(item) != ENTRY_FIELDS or not valid_name(item["file"]) or item["file"].casefold() in names:
            raise ValueError("MATERIAL_STAGE_ENTRY_INVALID")
        if item["runtimeMode"] not in {"NATIVE", "DOCKER"} or not isinstance(item["sha256"], str) or re.fullmatch(r"[a-f0-9]{64}", item["sha256"]) is None or type(item["byteSize"]) is not int or not 0 < item["byteSize"] <= ARCHIVE_LIMIT:
            raise ValueError("MATERIAL_STAGE_ENTRY_INVALID")
        for field, limit in [("recipeKey", 64), ("recipeVersion", 128), ("recipeRevision", 128), ("name", 160)]:
            value = item[field]
            if not isinstance(value, str) or not value.strip() or len(value) > limit or any(char in value for char in "\r\n\x00"):
                raise ValueError("MATERIAL_STAGE_ENTRY_INVALID")
        names.add(item["file"].casefold())
        total += item["byteSize"]
    if total > LIBRARY_LIMIT:
        raise ValueError("MATERIAL_STAGE_LIBRARY_LIMIT")
    output = protected_path(output, True)
    destination = output / "materials"
    if destination.exists() or destination.is_symlink():
        protected_path(destination, True)
    else:
        destination.mkdir(mode=0o700)
    deadline = time.monotonic() + 900
    for item in entries:
        source, target = library / item["file"], destination / item["file"]
        same = target.exists() and os.path.samefile(source, target)
        copy_checked(source, item["byteSize"], item["sha256"], None if same else target, deadline)
    # Publish the original bounded manifest last, after all declared bytes pass.
    checksum = hashlib.sha256(raw).hexdigest()
    if destination / "library.json" != manifest:
        copy_checked(manifest, len(raw), checksum, destination / "library.json", deadline)
    return {"files": ["materials/library.json", *["materials/" + item["file"] for item in entries]], "materials": len(entries), "bytes": total}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--library", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--optional", action="store_true", help="skip absent default library.json")
    args = parser.parse_args()
    print(json.dumps(stage(args.library, args.output, args.optional), ensure_ascii=False))


if __name__ == "__main__":
    main()
