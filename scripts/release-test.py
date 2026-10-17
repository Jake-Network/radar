#!/usr/bin/env python3
import importlib.util
import hashlib
import json
import subprocess
import sys
from unittest import mock
import pathlib
import tarfile
import tempfile
import unittest
import zipfile

spec=importlib.util.spec_from_file_location("release", pathlib.Path(__file__).with_name("release.py"))
release=importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class ArchiveTests(unittest.TestCase):
    def test_dependency_json_uses_utf8_paths_on_native_locale(self):
        # Go emits UTF-8 even when Python's native Windows locale is CP949 or
        # CP1252. Exercise a real subprocess pipe and a non-ASCII license path.
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            dependency = root / "dépendance"
            dependency.mkdir()
            (dependency / "LICENSE").write_text("fixture notice", encoding="utf-8")
            stage = root / "stage"
            stage.mkdir()
            payload = json.dumps({"Module": {"Path": "example.com/fixture", "Dir": str(dependency), "Version": "v1.0.0"}}, ensure_ascii=False).encode("utf-8")
            actual_check_output = subprocess.check_output
            def go_output(argv, **kwargs):
                self.assertEqual(argv[:4], ["go", "list", "-deps", "-json"])
                return actual_check_output([sys.executable, "-c", "import sys; sys.stdout.buffer.write(" + repr(payload) + ")"], **kwargs)
            with mock.patch.object(release.subprocess, "check_output", side_effect=go_output):
                release.dependency_notices(stage)
            self.assertEqual((stage / "licenses/example.com_fixture@v1.0.0/LICENSE").read_text(encoding="utf-8"), "fixture notice")

    def test_portable_archive_metadata_and_notices(self):
        for target in sorted(release.TARGETS):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as tmp:
                root=pathlib.Path(tmp); stage=root/"stage";stage.mkdir()
                exe="radar.exe" if target.startswith("windows") else "radar"
                (stage/exe).write_bytes(b"fixture")
                (stage/"LICENSE").write_text("fixture notice")
                (stage/"VERSION.txt").write_bytes(b"radar 0.4.0-test\n")
                (stage/"licenses").mkdir()
                (stage/"licenses"/"dependency-LICENSE").write_text("dependency notice")
                out=root/(target+(".zip" if target.startswith("windows") else ".tar.gz"))
                release.archive(stage,out,target)
                first=out.read_bytes();release.archive(stage,out,target)
                self.assertEqual(first,out.read_bytes())
                self.assertTrue(out.with_name(out.name+".sha256").is_file())
                self.assertEqual(out.with_name(out.name+".sha256").read_text(), hashlib.sha256(first).hexdigest()+"  "+out.name+"\n")
                if target.startswith("windows"):
                    with zipfile.ZipFile(out) as z:
                        self.assertEqual(z.read("radar-"+target+"/"+exe), b"fixture")
                        self.assertIn("radar-"+target+"/LICENSE",z.namelist())
                        self.assertEqual(z.read("radar-"+target+"/VERSION.txt"), b"radar 0.4.0-test\n")
                        self.assertEqual(z.read("radar-"+target+"/licenses/dependency-LICENSE"), b"dependency notice")
                        self.assertTrue(all(i.date_time == (1980, 1, 1, 0, 0, 0) for i in z.infolist()))
                else:
                    with tarfile.open(out) as t:
                        info=t.getmember("radar-"+target+"/radar")
                        self.assertEqual(info.mode,0o755)
                        self.assertEqual(info.uid,0)
                        self.assertEqual(info.mtime,0)
                        self.assertFalse(info.uname)
                        self.assertEqual(t.extractfile("radar-"+target+"/VERSION.txt").read(), b"radar 0.4.0-test\n")
                        self.assertEqual(t.extractfile("radar-"+target+"/licenses/dependency-LICENSE").read(), b"dependency notice")

    def test_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=pathlib.Path(tmp);stage=root/"stage";stage.mkdir();(stage/"bad").symlink_to("outside")
            with self.assertRaises(ValueError):release.archive(stage,root/"out.tar.gz","linux_amd64")

if __name__=="__main__":unittest.main()
