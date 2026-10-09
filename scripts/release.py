#!/usr/bin/env python3
"""Native CGO packaging; GOOS/GOARCH alone never establish a supported build."""
import gzip
import hashlib
import io
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
TARGETS = {"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64", "windows_amd64"}


def host_target():
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system(), "unsupported")
    arch = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64", "ARM64": "arm64"}.get(platform.machine(), "unsupported")
    return system + "_" + arch


def archive(stage, output, target, epoch=0):
    """Sorted archives with normalized metadata and no host-specific paths."""
    top = "radar-" + target
    paths = [stage] + sorted(stage.rglob("*"))
    for p in paths:
        if p.is_symlink() or not (p.is_dir() or p.is_file()):
            raise ValueError("Refusing non-regular release member: " + str(p.relative_to(stage)))
    executable = "radar.exe" if target.startswith("windows_") else "radar"
    if target.startswith("windows_"):
        with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for p in paths:
                name = top + ("/" + p.relative_to(stage).as_posix() if p != stage else "")
                if p.is_dir():
                    name += "/"
                item = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
                item.create_system = 3
                mode = 0o40755 if p.is_dir() else 0o100644
                item.external_attr = mode << 16
                item.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(item, b"" if p.is_dir() else p.read_bytes())
    else:
        with open(output, "wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as gz, tarfile.open(fileobj=gz, mode="w", format=tarfile.PAX_FORMAT) as tar:
            for p in paths:
                name = top + ("/" + p.relative_to(stage).as_posix() if p != stage else "")
                item = tar.gettarinfo(str(p), arcname=name)
                item.uid = item.gid = 0
                item.uname = item.gname = ""
                item.mtime = epoch
                item.mode = 0o755 if p.is_dir() or name == top + "/" + executable or name.endswith(".sh") else 0o644
                if p.is_file():
                    with p.open("rb") as data:
                        tar.addfile(item, data)
                else:
                    tar.addfile(item)
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    output.with_name(output.name + ".sha256").write_text(digest + "  " + output.name + "\n", encoding="utf-8")


def dependency_notices(stage):
    data = subprocess.check_output(["go", "list", "-deps", "-json", "./cmd/radar"], cwd=ROOT, text=True)
    decoder = json.JSONDecoder()
    modules = {}
    while data.strip():
        item, end = decoder.raw_decode(data.lstrip())
        data = data.lstrip()[end:]
        m = item.get("Module", {})
        if m.get("Dir") and not m.get("Main"):
            modules[m["Path"]] = m
    for name, m in sorted(modules.items()):
        source = pathlib.Path(m["Dir"])
        notices = [p for p in source.iterdir() if p.is_file() and p.name.upper().startswith(("LICENSE", "COPYING", "COPYRIGHT", "NOTICE"))]
        if not notices:
            raise ValueError("Dependency license not found: " + name)
        dest = stage / "licenses" / (name.replace("/", "_") + "@" + m.get("Version", ""))
        dest.mkdir(parents=True, exist_ok=True)
        for p in notices:
            shutil.copyfile(p, dest / p.name)
    (stage / "DEPENDENCIES.txt").write_text("".join(name + " " + m.get("Version", "") + "\n" for name, m in sorted(modules.items())), encoding="utf-8")


def package(output, version):
    config = json.loads(subprocess.check_output(["go", "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED"], text=True))
    target = config["GOOS"] + "_" + config["GOARCH"]
    if target not in TARGETS or target != host_target() or config["CGO_ENABLED"] != "1":
        raise ValueError("Native supported host with CGO_ENABLED=1 required; host " + host_target() + ", target " + target)
    if version and not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?", version):
        raise ValueError("Version must be X.Y.Z without leading v")
    output = pathlib.Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="radar-release-") as temporary:
        stage = pathlib.Path(temporary) / ("radar-" + target)
        stage.mkdir()
        binary = stage / ("radar.exe" if target.startswith("windows_") else "radar")
        flags = "-buildid="
        if version:
            flags += " -X github.com/Jake-Network/radar/internal/cli.Version=" + version
        if target.startswith("windows_"):
            flags += " -linkmode external -extldflags=-static"
        subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=" + flags, "-o", str(binary), "./cmd/radar"], cwd=ROOT, check=True)
        actual = subprocess.check_output([str(binary), "version"], text=True).strip()
        if version and actual != "radar " + version:
            raise ValueError("Packaged executable version mismatch")
        (stage / "VERSION.txt").write_text(actual + "\n", encoding="utf-8")
        for name in ("LICENSE", "README.md", "CONTRIBUTING.md"):
            shutil.copyfile(ROOT / name, stage / name)
        for name in ("third_party", "docs", "integrations", "schemas", "scripts"):
            shutil.copytree(ROOT / name, stage / name, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        shutil.copytree(ROOT / "examples", stage / "examples", ignore=shutil.ignore_patterns(".radar", "__pycache__", "node_modules", "target"))
        for binding in (ROOT / "examples").rglob(".radar/contracts.json"):
            dest = stage / "examples" / binding.relative_to(ROOT / "examples")
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(binding, dest)
        dependency_notices(stage)
        subprocess.run([sys.executable, str(ROOT / "scripts/smoke-release.py"), str(binary)], check=True)
        suffix = ".zip" if target.startswith("windows_") else ".tar.gz"
        artifact = output / ("radar-" + target + suffix)
        archive(stage, artifact, target, int(os.environ.get("SOURCE_DATE_EPOCH", "0")))
        print("Release archive: " + str(artifact))
        return artifact


if __name__ == "__main__":
    try:
        package(sys.argv[1] if len(sys.argv) > 1 else ROOT / "dist", sys.argv[2] if len(sys.argv) > 2 else os.environ.get("RADAR_VERSION", ""))
    except (ValueError, OSError, subprocess.SubprocessError) as err:
        raise SystemExit("Release failed: " + str(err))
