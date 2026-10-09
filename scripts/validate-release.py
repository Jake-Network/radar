#!/usr/bin/env python3
"""Check checksum, members, installed version and parsers on the native host."""
import hashlib
import os
import pathlib
import stat
import subprocess
import sys
import tarfile
import tempfile
import zipfile

from importlib.util import module_from_spec, spec_from_file_location
spec = spec_from_file_location("release", pathlib.Path(__file__).with_name("release.py"))
release = module_from_spec(spec)
spec.loader.exec_module(release)
target = release.host_target()
suffix = ".zip" if target.startswith("windows_") else ".tar.gz"
artifact = pathlib.Path(sys.argv[1]) / ("radar-" + target + suffix)
expected = artifact.with_name(artifact.name + ".sha256").read_text().strip().split()
assert len(expected) == 2 and expected[1] == artifact.name
assert hashlib.sha256(artifact.read_bytes()).hexdigest() == expected[0]

with tempfile.TemporaryDirectory(prefix="radar-installed-smoke-") as tmp:
    root = pathlib.Path(tmp).resolve()  # macOS temp is below the /var symlink
    seen = set()
    total = 0
    def destination(name, size):
        global total
        parts = name.rstrip("/").split("/")
        if not parts or parts[0] != "radar-" + target or any(p in ("", ".", "..") or "\\" in p or ":" in p for p in parts):
            raise ValueError("Unsafe archive path")
        if name in seen:
            raise ValueError("Duplicate archive member")
        seen.add(name)
        total += size
        if total > 256 * 1024 * 1024 or len(seen) > 10000:
            raise ValueError("Archive smoke budget exceeded")
        p = root.joinpath(*parts)
        if not p.resolve().is_relative_to(root):
            raise ValueError("Archive escapes extraction directory")
        return p
    if suffix == ".zip":
        with zipfile.ZipFile(artifact) as z:
            for entry in z.infolist():
                kind = (entry.external_attr >> 16) & 0o170000
                if kind not in (0, stat.S_IFDIR, stat.S_IFREG):
                    raise ValueError("Special archive member")
                p = destination(entry.filename, entry.file_size)
                if entry.is_dir():
                    p.mkdir(parents=True, exist_ok=True)
                else:
                    p.parent.mkdir(parents=True, exist_ok=True)
                    p.write_bytes(z.read(entry))
    else:
        with tarfile.open(artifact) as t:
            for entry in t:
                if not (entry.isfile() or entry.isdir()):
                    raise ValueError("Special archive member")
                p = destination(entry.name, entry.size)
                if entry.isdir():
                    p.mkdir(parents=True, exist_ok=True)
                else:
                    p.parent.mkdir(parents=True, exist_ok=True)
                    p.write_bytes(t.extractfile(entry).read())
                    p.chmod(entry.mode)
    stage = root / ("radar-" + target)
    for required in ("LICENSE", "DEPENDENCIES.txt", "VERSION.txt", "third_party", "licenses"):
        assert (stage / required).exists(), "Missing release notice: " + required
    binary = stage / ("radar.exe" if suffix == ".zip" else "radar")
    # Artifact smoke does not find compilers/DLLs through the build environment.
    env = dict(os.environ)
    env["PATH"] = os.pathsep.join(p for p in env.get("PATH", "").split(os.pathsep) if not any(x in p.lower() for x in ("msys", "mingw", "ucrt64", "go/bin")))
    version = subprocess.check_output([str(binary), "version"], text=True, env=env).strip()
    assert version == "radar " + sys.argv[2], version
    assert (stage / "VERSION.txt").read_text(encoding="utf-8").strip() == version, "Archive version metadata mismatch"
    subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name("smoke-release.py")), str(binary)], env=env, check=True)
print("Installed archive checksum, notices, version and native parser checks passed: " + target)
