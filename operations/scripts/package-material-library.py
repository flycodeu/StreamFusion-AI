#!/usr/bin/env python3
"""Build a release-sidecar library from this binary's immutable recipe catalog.

The output directory must be new. Original vendor bytes are copied or downloaded
with fixed size/SHA-256 checks; no package or install script is executed. Multi-
part native recipes use a deterministic tar of their original named parts.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import time
import urllib.request

ARCHIVE_LIMIT = 2 << 30
LIBRARY_LIMIT = 16 << 30
DEFAULT_RECIPES = (
    "redis@7.0.15-1ubuntu0.24.04.5",
    "nginx@1.24.0-2ubuntu7.19",
    "java@8",
    "docker@29.9.2-ubuntu24.04",
)


def file_digest(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def checked_part(path: Path, part: dict) -> bool:
    return (
        not path.is_symlink()
        and path.is_file()
        and path.stat().st_size == part["byteSize"]
        and file_digest(path) == part["sha256"]
    )


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        if not newurl.startswith("https://"):
            raise ValueError("MATERIAL_DOWNLOAD_REDIRECT_FORBIDDEN")
        return super().redirect_request(request, fp, code, msg, headers, newurl)


def obtain_part(part: dict, sources: list[Path], temp: Path, download: bool, deadline: float) -> Path:
    name = part["name"]
    if Path(name).name != name or any(char in name for char in "/\\:\r\n\x00"):
        raise ValueError("MATERIAL_PART_NAME_INVALID")
    for source in sources:
        candidate = source / name
        if candidate.exists() or candidate.is_symlink():
            if not checked_part(candidate, part):
                raise ValueError(f"MATERIAL_SOURCE_HASH_OR_SIZE_MISMATCH: {name}")
            return candidate
    if not download:
        raise ValueError(f"MATERIAL_SOURCE_MISSING: {name}; supply --source or --download")
    if not part["url"].startswith("https://"):
        raise ValueError("MATERIAL_DOWNLOAD_HTTPS_REQUIRED")
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("MATERIAL_DOWNLOAD_DEADLINE")
    output = temp / name
    opener = urllib.request.build_opener(HTTPSRedirect())
    request = urllib.request.Request(part["url"], headers={"User-Agent": "StreamFusion-Ops-Material-Packer/0.2"})
    with opener.open(request, timeout=min(60, remaining)) as response, output.open("xb") as stream:
        size = 0
        while block := response.read(1 << 20):
            size += len(block)
            if size > part["byteSize"] or size > ARCHIVE_LIMIT or time.monotonic() > deadline:
                raise ValueError(f"MATERIAL_DOWNLOAD_SIZE_OR_DEADLINE: {name}")
            stream.write(block)
    if not checked_part(output, part):
        raise ValueError(f"MATERIAL_DOWNLOAD_HASH_OR_SIZE_MISMATCH: {name}")
    return output


def native_entry(recipe: dict, sources: list[Path], temp: Path, destination: Path, download: bool, deadline: float) -> dict:
    parts = recipe["native"]["parts"]
    if not parts or sum(part["byteSize"] for part in parts) > ARCHIVE_LIMIT:
        raise ValueError("MATERIAL_PARTS_LIMIT")
    part_dir = temp / (recipe["key"] + "-" + recipe["version"] + "-parts")
    part_dir.mkdir()
    originals = [obtain_part(part, sources, part_dir, download, deadline) for part in parts]
    name = f'{recipe["key"]}-{recipe["version"]}-native' + (".tar" if len(parts) > 1 else "-" + parts[0]["name"])
    output = destination / name
    if len(parts) == 1:
        shutil.copyfile(originals[0], output)
    else:
        with tarfile.open(output, "x", format=tarfile.USTAR_FORMAT) as archive:
            for part, original in zip(parts, originals, strict=True):
                info = tarfile.TarInfo(part["name"])
                info.size, info.mode = part["byteSize"], 0o600
                with original.open("rb") as stream:
                    archive.addfile(info, stream)
    return entry(recipe, output, "NATIVE")


def entry(recipe: dict, file: Path, mode: str) -> dict:
    size = file.stat().st_size
    if size < 1 or size > ARCHIVE_LIMIT:
        raise ValueError("MATERIAL_ARCHIVE_LIMIT")
    return {
        "recipeKey": recipe["key"], "recipeVersion": recipe["version"],
        "recipeRevision": recipe["revision"], "runtimeMode": mode,
        "name": f'{recipe["name"]} {recipe["version"]} ' + ("原生材料" if mode == "NATIVE" else "离线镜像"),
        "file": file.name, "sha256": file_digest(file), "byteSize": size,
    }


def build(args: argparse.Namespace) -> None:
    if args.catalog:
        catalog = json.loads(args.catalog.read_text(encoding="utf-8-sig"))
    else:
        command = subprocess.run([str(args.ops.resolve()), "material-catalog"], check=True, capture_output=True, timeout=30)
        if len(command.stdout) > 1 << 20:
            raise ValueError("MATERIAL_CATALOG_TOO_LARGE")
        catalog = json.loads(command.stdout)
    recipes = {recipe["key"] + "@" + recipe["version"]: recipe for recipe in catalog}
    selections = args.recipe if args.recipe is not None else list(DEFAULT_RECIPES)
    if args.out.exists() or args.out.is_symlink():
        raise ValueError("MATERIAL_OUTPUT_EXISTS")
    parent = args.out.resolve().parent
    parent.mkdir(parents=True, exist_ok=True)
    deadline = time.monotonic() + 900
    with tempfile.TemporaryDirectory(prefix=".material-library-", dir=parent) as temporary:
        temp = Path(temporary)
        output = temp / "materials"
        output.mkdir()
        materials = []
        for selection in selections:
            recipe = recipes[selection]
            if "NATIVE" not in recipe["runtimeModes"] or not recipe.get("native"):
                raise ValueError(f"MATERIAL_NATIVE_RECIPE_UNAVAILABLE: {selection}")
            materials.append(native_entry(recipe, args.source, temp, output, args.download, deadline))
        for specification in args.docker_archive:
            selection, path_text = specification.split("=", 1)
            recipe, original = recipes[selection], Path(path_text)
            if "DOCKER" not in recipe["runtimeModes"] or original.is_symlink() or not original.is_file():
                raise ValueError("MATERIAL_DOCKER_ARCHIVE_INVALID")
            target = output / f'{recipe["key"]}-{recipe["version"]}-docker.tar'
            if original.stat().st_size > ARCHIVE_LIMIT:
                raise ValueError("MATERIAL_ARCHIVE_LIMIT")
            shutil.copyfile(original, target)
            materials.append(entry(recipe, target, "DOCKER"))
        if len(materials) > 100 or sum(item["byteSize"] for item in materials) > LIBRARY_LIMIT:
            raise ValueError("MATERIAL_LIBRARY_QUOTA")
        manifest = {"schemaVersion": 1, "materials": materials}
        (output / "library.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        os.rename(output, args.out)
    print(json.dumps({"directory": str(args.out.resolve()), "materials": len(materials), "bytes": sum(item["byteSize"] for item in materials)}, ensure_ascii=False))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    default_ops = Path(__file__).resolve().parent.parent / ("streamfusion-ops.exe" if os.name == "nt" else "streamfusion-ops")
    parser.add_argument("--ops", type=Path, default=default_ops, help="binary providing read-only material-catalog")
    parser.add_argument("--catalog", type=Path, help="explicit material-catalog JSON instead of invoking a binary")
    parser.add_argument("--source", type=Path, action="append", default=[], help="directory of unchanged vendor files; repeatable")
    parser.add_argument("--out", type=Path, required=True, help="new release-sidecar materials directory")
    parser.add_argument("--recipe", action="append", help="native key@version; repeatable; default supplied Redis/Nginx/JDK8 and Docker Engine")
    parser.add_argument("--docker-archive", action="append", default=[], metavar="KEY@VERSION=FILE", help="add a Docker-save image; pinned image/layers are checked on import")
    parser.add_argument("--download", action="store_true", help="download missing fixed original parts over HTTPS (15 minute total bound)")
    args = parser.parse_args()
    build(args)


if __name__ == "__main__":
    main()
