"""Pairing regressions using synthetic evidence; no external code executes."""
import copy
import gzip
import hashlib
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

DIRECTORY = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('external_report', DIRECTORY / 'report.py')
report = importlib.util.module_from_spec(spec)
sys.path.insert(0, str(DIRECTORY))
try:
    spec.loader.exec_module(report)
finally:
    sys.path.pop(0)


def evidence():
    def suite(status, commit):
        return {'status': status, 'commands': [], 'source_integrity': {
            'checked': True, 'source_modified': False,
            'before_commit': commit, 'after_commit': commit,
            'tracked_changes_before': [], 'tracked_changes_after': []}}

    row = {'id': 'challenge', 'combined_tree': 'c' * 40,
           'combined_tests': suite('failed', 'e' * 40),
           'branches': [{'commit': 'a' * 40, 'tree': 'b' * 40,
                         'tests': suite('passed', 'a' * 40)}],
           'ground_truth_status': 'failed', 'ground_truth_usable': True,
           'integration_only': False, 'test_file_oracle_available': False,
           'failing_test_files': [], 'modes': [{'mode': 'full', 'repeat': 0,
               'report': {'candidate_tree': 'c' * 40,
                          'executions': [{'duration_ms': 500}]},
               'end_to_end_seconds': 2, 'conventional_baseline_seconds': 3,
               'verdict': 'fail', 'selection_available': False}]}
    artifact = {'repository': {'name': 'Synthetic project',
        'url': 'https://example.invalid/project', 'revision': 'f' * 40},
        'provenance': {'image': 'sha256:' + '1' * 64},
        'baseline': [{'tests': suite('passed', 'd' * 40)}], 'rows': [row]}
    return copy.deepcopy(artifact), copy.deepcopy(artifact)


class ExternalReportTests(unittest.TestCase):
    def test_pair_preserves_inputs_and_original_runtime_measurements(self):
        measured, oracle = evidence()
        oracle['rows'][0]['modes'][0]['end_to_end_seconds'] = 100
        originals = copy.deepcopy((measured, oracle))
        rows = report.pair(measured, oracle)
        self.assertEqual((measured, oracle), originals)
        self.assertEqual(rows[0]['modes'][0]['end_to_end_seconds'], 2)
        self.assertEqual(rows[0]['modes'][0]['conventional_baseline_seconds'], 3)
        self.assertEqual(rows[0]['modes'][0]['test_execution_seconds'], .5)

    def test_revision_image_and_candidate_mismatches_are_rejected(self):
        for field in ('revision', 'image', 'combined_tree', 'radar_tree'):
            with self.subTest(field=field):
                measured, oracle = evidence()
                if field == 'revision':
                    oracle['repository']['revision'] = '0' * 40
                elif field == 'image':
                    oracle['provenance']['image'] = 'sha256:' + '2' * 64
                elif field == 'combined_tree':
                    oracle['rows'][0]['combined_tree'] = '0' * 40
                else:
                    measured['rows'][0]['modes'][0]['report']['candidate_tree'] = '0' * 40
                with self.assertRaises(ValueError):
                    report.pair(measured, oracle)

    def test_same_combined_tree_does_not_validate_different_branch_inputs(self):
        measured, oracle = evidence()
        oracle['rows'][0]['branches'][0]['tree'] = '0' * 40
        with self.assertRaises(ValueError):
            report.pair(measured, oracle)

    def test_missing_tree_identities_cannot_match_by_absence(self):
        for field in ('combined', 'branch'):
            with self.subTest(field=field):
                measured, oracle = evidence()
                for artifact in (measured, oracle):
                    row = artifact['rows'][0]
                    if field == 'combined':
                        del row['combined_tree']
                        del row['modes'][0]['report']['candidate_tree']
                    else:
                        del row['branches'][0]['tree']
                with self.assertRaises(ValueError):
                    report.pair(measured, oracle)

    def test_missing_or_modified_integrity_is_rejected_for_every_suite(self):
        for location in ('baseline', 'branch', 'combined'):
            for defect in ('missing', 'unchecked', 'modified', 'dirty_before',
                           'dirty_after', 'head_changed', 'missing_heads', 'missing_paths'):
                with self.subTest(location=location, defect=defect):
                    measured, oracle = evidence()
                    suite = (oracle['baseline'][0]['tests'] if location == 'baseline' else
                             oracle['rows'][0]['branches'][0]['tests'] if location == 'branch' else
                             oracle['rows'][0]['combined_tests'])
                    guard = suite['source_integrity']
                    if defect == 'missing':
                        del suite['source_integrity']
                    elif defect == 'unchecked':
                        guard['checked'] = False
                    elif defect == 'modified':
                        guard['source_modified'] = True
                    elif defect == 'dirty_before':
                        guard['tracked_changes_before'] = ['source.py']
                    elif defect == 'dirty_after':
                        guard['tracked_changes_after'] = ['source.py']
                    elif defect == 'missing_heads':
                        del guard['before_commit']
                        del guard['after_commit']
                    elif defect == 'missing_paths':
                        del guard['tracked_changes_before']
                        del guard['tracked_changes_after']
                    else:
                        guard['after_commit'] = '0' * 40
                    with self.assertRaises(ValueError):
                        report.pair(measured, oracle)

    def test_unavailable_baseline_cannot_validate_truth(self):
        measured, oracle = evidence()
        oracle['baseline'][0]['tests']['status'] = 'environment_unavailable'
        with self.assertRaises(ValueError):
            report.pair(measured, oracle)

    def test_unavailable_candidate_never_enters_accuracy_denominator(self):
        measured, oracle = evidence()
        row = oracle['rows'][0]
        row['combined_tests']['status'] = 'timeout'
        row['ground_truth_status'] = 'timeout'
        row['ground_truth_usable'] = False
        try:
            paired = report.pair(measured, oracle)
        except ValueError:
            return
        summary = report.run.summarize(paired)['full']
        self.assertEqual(summary['observed_ground_truth'], 0)
        self.assertIsNone(summary['mutation_defect_detection']['value'])

    def test_output_cannot_overwrite_either_input_or_markdown_alias(self):
        for target in ('measurements', 'oracle', 'markdown_alias', 'symlink'):
            with self.subTest(target=target), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                measured, oracle = evidence()
                measured_path = root / ('result.md' if target == 'markdown_alias' else 'measurements.json')
                oracle_path = root / 'oracle.json'
                measured_path.write_text(json.dumps(measured))
                oracle_path.write_text(json.dumps(oracle))
                original_bytes = (measured_path.read_bytes(), oracle_path.read_bytes())
                output = measured_path if target == 'measurements' else oracle_path
                if target == 'markdown_alias':
                    output = root / 'result.json'
                elif target == 'symlink':
                    output = root / 'alias.json'
                    output.symlink_to(measured_path)
                argv = ['report.py', '--measurements', str(measured_path),
                        '--oracle', str(oracle_path), '--output', str(output)]
                with patch.object(sys, 'argv', argv), self.assertRaises((ValueError, SystemExit)):
                    report.main()
                self.assertEqual((measured_path.read_bytes(), oracle_path.read_bytes()), original_bytes)

    def test_compressed_input_hash_identifies_original_immutable_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'evidence.json.gz'
            artifact = {'sample': 'immutable'}
            raw = gzip.compress(json.dumps(artifact).encode(), mtime=0)
            path.write_bytes(raw)
            loaded, digest = report.read(path)
            self.assertEqual(loaded, artifact)
            self.assertEqual(digest, hashlib.sha256(raw).hexdigest())
            self.assertEqual(path.read_bytes(), raw)


if __name__ == '__main__':
    unittest.main()
