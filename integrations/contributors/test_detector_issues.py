import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('detector_issues', HERE / 'detector_issues.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class DetectorDrafts(unittest.TestCase):
    def test_fifteen_bounded_issues_have_existing_owners_and_negative_fixtures(self):
        self.assertEqual(len(module.DETECTORS), 15)
        self.assertEqual(len({entry[0] for entry in module.DETECTORS}), 15)
        for entry in module.DETECTORS:
            self.assertTrue((HERE.parent.parent / entry[2]).is_dir())
            rendered = module.render(entry)
            self.assertEqual((HERE / 'issues' / (entry[0] + '.md')).read_text(), rendered)
            for required in ('Base-existing:', 'Single-branch:', 'Malformed:', 'Portability:', 'unknown', 'no repository execution'):
                self.assertIn(required, rendered)

    def test_generator_does_not_overwrite_existing_drafts(self):
        with tempfile.TemporaryDirectory() as tmp:
            command = [sys.executable, str(HERE / 'detector_issues.py'), '--out', tmp]
            first = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(first.returncode, 0, first.stderr)
            self.assertEqual(len(list(Path(tmp).glob('*.md'))), 15)
            self.assertEqual(subprocess.run(command, capture_output=True).returncode, 2)


if __name__ == '__main__':
    unittest.main()
