#!/usr/bin/env python3
import importlib.util
import pathlib
import tarfile
import tempfile
import unittest
import zipfile

spec=importlib.util.spec_from_file_location("release", pathlib.Path(__file__).with_name("release.py"))
release=importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class ArchiveTests(unittest.TestCase):
    def test_portable_archive_metadata_and_notices(self):
        for target in ("linux_amd64", "darwin_arm64", "windows_amd64"):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as tmp:
                root=pathlib.Path(tmp); stage=root/"stage";stage.mkdir()
                exe="radar.exe" if target.startswith("windows") else "radar"
                (stage/exe).write_bytes(b"fixture")
                (stage/"LICENSE").write_text("fixture notice")
                out=root/(target+(".zip" if target.startswith("windows") else ".tar.gz"))
                release.archive(stage,out,target)
                first=out.read_bytes();release.archive(stage,out,target)
                self.assertEqual(first,out.read_bytes())
                self.assertTrue(out.with_name(out.name+".sha256").is_file())
                if target.startswith("windows"):
                    with zipfile.ZipFile(out) as z:
                        self.assertEqual(z.read("radar-"+target+"/"+exe), b"fixture")
                        self.assertIn("radar-"+target+"/LICENSE",z.namelist())
                else:
                    with tarfile.open(out) as t:
                        info=t.getmember("radar-"+target+"/radar")
                        self.assertEqual(info.mode,0o755)
                        self.assertEqual(info.uid,0)
                        self.assertEqual(info.mtime,0)
                        self.assertFalse(info.uname)

    def test_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=pathlib.Path(tmp);stage=root/"stage";stage.mkdir();(stage/"bad").symlink_to("outside")
            with self.assertRaises(ValueError):release.archive(stage,root/"out.tar.gz","linux_amd64")

if __name__=="__main__":unittest.main()
