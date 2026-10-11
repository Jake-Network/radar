"""Offline harness regressions; these tests do not execute external repositories."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
import json
import hashlib
import io
import tarfile
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('external_run', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ExternalRunnerTests(unittest.TestCase):
    def test_writable_image_cache_mounts_are_bounded_and_confined(self):
        self.assertEqual(runner.cache_tmpfs_options(['/opt/hono/node_modules/.vite']),
            ['--tmpfs=/opt/hono/node_modules/.vite:rw,nosuid,nodev,noexec,size=128m,mode=1777'])
        for path in ('/tmp/cache', '/opt/../credentials', '/opt/cache:rw', '/opt/cache,size=4g', '/opt/cache\nsecret', '/opt//cache'):
            with self.assertRaises(ValueError):
                runner.cache_tmpfs_options([path])

    def test_container_affinity_matches_cpu_quota(self):
        with patch.object(runner.Path, 'exists', return_value=True), \
             patch.object(runner.os, 'sched_getaffinity', side_effect=[{2, 4, 6, 8}, {2, 4}]), \
             patch.object(runner.os, 'sched_setaffinity') as apply:
            record = runner.worker_affinity()
        apply.assert_called_once_with(0, [2, 4])
        self.assertTrue(record['applied'])
        self.assertEqual(record['effective_cpus'], [2, 4])

    def test_oracle_only_runs_protected_native_branches_without_radar_metrics(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            for filename in ('a.py', 'b.py'):
                (repo / filename).write_text('VALUE = 0\n')
            (repo / 'test_gate.py').write_text('import unittest, a, b\nclass NativeTest(unittest.TestCase):\n def test_budget(self): self.assertLessEqual(a.VALUE+b.VALUE, 1)\n')
            runner.ev.git(repo, 'init', '-q')
            runner.ev.git(repo, 'add', '.')
            runner.ev.git(repo, 'commit', '-qm', 'trusted oracle-only regression')
            manifest = {'repository': {'revision': runner.ev.git(repo, 'rev-parse', 'HEAD')},
                'full_suite': [{'argv': [sys.executable, '-m', 'unittest', 'test_gate'], 'recognizer': 'unittest'}],
                'cases': [{'id': 'native-integration', 'agents': [[{'path': filename, 'find': 'VALUE = 0', 'replace': 'VALUE = 1'}]
                                                               for filename in ('a.py', 'b.py')]}]}
            with patch.object(runner.ev, 'run', wraps=runner.ev.run) as execute:
                result = runner.evaluate_worker(manifest, repo, Path('/unused'), 10, 1, oracle_only=True)
            row = result['rows'][0]
            self.assertEqual(result['execution_kind'], 'oracle_only')
            self.assertTrue(row['integration_only'])
            self.assertTrue(row['ground_truth_usable'])
            self.assertEqual(row['modes'], [])
            for observation in [result['baseline'][0]['tests'], row['combined_tests'], *[branch['tests'] for branch in row['branches']]]:
                self.assertTrue(observation['source_integrity']['checked'])
                self.assertFalse(observation['source_integrity']['source_modified'])
            self.assertFalse(any('gate' in call.args[0] or 'check' in call.args[0] for call in execute.call_args_list))
            self.assertIsNone(result['summary']['full']['mutation_defect_detection']['value'])
            self.assertEqual(result['summary']['full']['known_integration_only_defects'], 1)
            self.assertEqual(result['summary']['full']['observed_ground_truth'], 1)

    def test_native_suite_cannot_establish_truth_after_modifying_tracked_source(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            source = repo / 'source.py'
            source.write_text('original')
            runner.ev.git(repo, 'init', '-q')
            runner.ev.git(repo, 'add', '.')
            runner.ev.git(repo, 'commit', '-qm', 'trusted regression source')
            observation = runner.suite(repo, [{'argv': [sys.executable, '-c',
                "from pathlib import Path; Path('source.py').write_text('changed'); print('1 passed')"],
                'recognizer': 'pytest'}], 10)
            self.assertEqual(observation['commands'][0]['recognized_result'], 'passed')
            self.assertEqual(observation['status'], 'source_modified')
            self.assertEqual(observation['source_integrity']['tracked_changes_after'], ['source.py'])

    def test_attribution_without_durations_invalidates_total_execution_timing(self):
        report = {'executions': [{'duration_ms': 26000}],
                  'observed_attributions': [{'runs': [{'status': 'passed'}]}]}
        timing = runner.execution_timing(report, 105)
        self.assertEqual(timing['selected_test_execution_seconds'], 26)
        self.assertIsNone(timing['test_execution_seconds'])
        self.assertIsNone(timing['non_execution_seconds'])
        self.assertIn('Attribution', timing['test_execution_unavailable_reason'])
        report['observed_attributions'] = []
        self.assertEqual(runner.execution_timing(report, 105)['test_execution_seconds'], 26)

    def test_junit_labels_require_native_outcome_and_safe_file_bindings(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'tests').mkdir()
            for filename in ('test_a.py', 'test_b.py'):
                (root / 'tests' / filename).write_text('# trusted test file')
            report = root / 'result.xml'
            record = {'status': 'completed', 'recognized_result': 'failed'}
            report.write_text('<testsuite><testcase file="tests/test_a.py"><failure/></testcase><testcase file="tests/test_b.py"/></testsuite>')
            result = runner.read_junit(report, root, record)
            self.assertTrue(result['available'])
            self.assertEqual(result['case_count'], 2)
            self.assertEqual(result['failing_test_files'], ['tests/test_a.py'])
            for content in ('<testsuite/>', '<testsuite><testcase/></testsuite>',
                            '<testsuite><testcase file="../escape"/></testsuite>',
                            '<!DOCTYPE x [<!ENTITY y "boom">]><testsuite/>',
                            '<testsuite><testcase file="test_a.py"/></testsuite>'):
                report.write_text(content)
                self.assertFalse(runner.read_junit(report, root, record)['available'])

    def test_junit_report_is_removed_before_every_execution(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report = root / 'report.xml'
            report.write_text('<testsuite><testcase file="stale.py"/></testsuite>')
            def execute(*args):
                self.assertFalse(report.exists())
                return [{'status': 'completed', 'recognized_result': 'passed', 'exit_code': 0,
                         'stdout': '1 passed', 'seconds': 0}]
            with patch.object(runner.ev, 'suite', side_effect=execute):
                observation = runner.suite(root, [{'argv': ['unused'], 'junit_report': 'report.xml'}], 1)
            self.assertEqual(observation['status'], 'passed')
            self.assertFalse(observation['commands'][0]['junit']['available'])

    def test_precision_includes_passing_controls(self):
        rows = []
        for state, failing, selected in [('failed', ['bad.py'], ['bad.py', 'extra.py']),
                                         ('passed', [], ['clean.py', 'another.py'])]:
            rows.append({'ground_truth_usable': True, 'ground_truth_status': state,
                'integration_only': False, 'test_file_oracle_available': True, 'failing_test_files': failing,
                'modes': [{'mode': 'full', 'repeat': 0, 'selection_available': True,
                           'selected_test_files': selected, 'verdict': 'fail' if failing else 'pass'}]})
        summary = runner.summarize(rows)['full']
        self.assertEqual(summary['test_selection_precision']['value'], .25)
        self.assertEqual(summary['test_selection_recall']['value'], 1)

    def test_legacy_tar_extraction_rejects_escapes_and_links(self):
        for name, kind in [('../outside', tarfile.REGTYPE), ('/absolute', tarfile.REGTYPE),
                           ('link', tarfile.SYMTYPE), ('hardlink', tarfile.LNKTYPE), ('device', tarfile.CHRTYPE)]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                archive = io.BytesIO()
                with tarfile.open(fileobj=archive, mode='w') as writer:
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = '/outside'
                    writer.addfile(member)
                archive.seek(0)
                with tarfile.open(fileobj=archive) as source:
                    data_filter = getattr(tarfile, 'data_filter', None)
                    if data_filter is not None:
                        del tarfile.data_filter
                    try:
                        with self.assertRaises(ValueError):
                            runner.extract_snapshot(source, Path(directory))
                    finally:
                        if data_filter is not None:
                            tarfile.data_filter = data_filter

    def test_legacy_tar_extraction_preserves_regular_files(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode='w') as writer:
                member = tarfile.TarInfo('sub/source.py')
                member.size = 4
                member.mode = 0o4755
                writer.addfile(member, io.BytesIO(b'test'))
            archive.seek(0)
            with tarfile.open(fileobj=archive) as source:
                data_filter = getattr(tarfile, 'data_filter', None)
                if data_filter is not None:
                    del tarfile.data_filter
                try:
                    runner.extract_snapshot(source, Path(directory))
                finally:
                    if data_filter is not None:
                        tarfile.data_filter = data_filter
            path = Path(directory) / 'sub/source.py'
            self.assertEqual(path.read_bytes(), b'test')
            self.assertFalse(path.stat().st_mode & 0o4000)

    def test_missing_inventory_uses_independent_files_not_radar_count(self):
        report = {'verification_proposal': {'inventory': {'tests': [{'path': 'test_a.py'}]},
                    'commands': [{'test_files': ['test_a.py'], 'tool_available': False}]},
                  'selection': {'inventory_test_files': 999,
                    'omitted': [{'reason': 'unsupported_configuration', 'test_files': ['test_c.py']}]}}
        metric = runner.inventory_metrics(report, None, ['test_a.py', 'test_b.py'])
        self.assertEqual(metric['missing_inventory_files'], ['test_b.py'])
        self.assertEqual(metric['unsupported_test_files'], ['test_c.py'])
        self.assertEqual(metric['unavailable_runner_test_files'], ['test_a.py'])
        self.assertIsNone(runner.inventory_metrics(report, None, None)['missing_inventory_files'])
        self.assertIsNone(runner.inventory_metrics({}, {}, ['test_a.py'])['missing_inventory_files'])
        self.assertEqual(runner.inventory_metrics({}, report, ['test_a.py', 'test_b.py'])['missing_inventory_files'], ['test_b.py'])

    def test_suite_retains_timeout_and_missing_dependency(self):
        for raw, output, exit_code, expected in [
            ('timeout', '', -9, 'timeout'),
            ('completed', '1 failed\nModuleNotFoundError: missing', 1, 'environment_unavailable'),
            ('completed', '1 passed; ModuleNotFoundError was expected', 0, 'passed')]:
            record = {'status': raw, 'recognized_result': 'passed' if exit_code == 0 else 'failed',
                      'exit_code': exit_code, 'stdout': output, 'seconds': 1}
            with tempfile.TemporaryDirectory() as directory, patch.object(runner.ev, 'suite', return_value=[record]):
                self.assertEqual(runner.suite(Path(directory), [{'argv': ['unused']}], 1)['status'], expected)

    def test_dependency_diagnostics_do_not_match_assertion_literals(self):
        for output in ("ModuleNotFoundError: No module named 'missing'", "E   ModuleNotFoundError: missing",
                       "Error: Cannot find module 'missing'", "Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'missing'",
                       'npm error code ENOENT', '/bin/sh: 1: vitest: not found'):
            self.assertTrue(runner.dependency_diagnostic(output), output)
        for output in ("E   AssertionError: expected ModuleNotFoundError: missing",
                       "assert 'Cannot find module' in output", "AssertionError: Cannot find package expected",
                       "assert code == 'ENOENT'", "assert text == 'checksum mismatch'",
                       "ImportError: cannot import name 'removed_function'", "1 failed; ModuleNotFoundError: quoted expected literal"):
            self.assertFalse(runner.dependency_diagnostic(output), output)
            record = {'status': 'completed', 'recognized_result': 'failed', 'exit_code': 1,
                      'stdout': output, 'seconds': 0}
            with tempfile.TemporaryDirectory() as directory, patch.object(runner.ev, 'suite', return_value=[record]):
                self.assertEqual(runner.suite(Path(directory), [{'argv': ['unused']}], 1)['status'], 'failed')

    def test_docker_enforces_offline_disposable_execution(self):
        command = runner.docker_command('sha256:' + 'a' * 64, Path('/tmp/source'),
            Path('/tmp/radar'), Path('/tmp/manifest'), Path('/tmp/output'), 10, 2, 'case')
        for flag in ('--network=none', '--read-only', '--cap-drop=ALL',
                     '--security-opt=no-new-privileges', '--pull=never', '--user=65534:65534'):
            self.assertIn(flag, command)
        mounts = [command[i + 1] for i, value in enumerate(command) if value == '--mount']
        self.assertEqual(sum('readonly' in mount for mount in mounts), 4)
        self.assertFalse(any('docker.sock' in argument for argument in command))
        self.assertFalse(any('HOME=' in argument and '/home/' in argument for argument in command))

    def test_mutable_image_tags_rejected(self):
        with self.assertRaisesRegex(ValueError, 'pinned'):
            runner.docker_command('python:latest', Path('/tmp/source'), Path('/tmp/radar'),
                Path('/tmp/manifest'), Path('/tmp/output'), 10, 2, 'case')

    def test_invocation_hashes_frozen_manifest_and_binary(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest, binary, output = root / 'manifest.json', root / 'radar', root / 'result.json'
            payload = {'schema_version': 1, 'repository': {'name': 'trusted smoke', 'revision': 'a' * 40},
                       'full_suite': [{'argv': ['unused']}], 'cases': [{'id': 'unused'}]}
            manifest.write_text(json.dumps(payload))
            binary.write_bytes(b'trusted test binary placeholder')
            expected = [hashlib.sha256(path.read_bytes()).hexdigest() for path in (manifest, binary)]

            def simulated_command(argv, cwd, timeout):
                if argv[0] == 'docker':
                    mounts = [argv[i + 1] for i, value in enumerate(argv) if value == '--mount']
                    output_mount = next(mount for mount in mounts if 'dst=/output' in mount)
                    artifact_dir = Path(output_mount.split('src=', 1)[1].split(',')[0])
                    manifest.write_text('modified concurrently')
                    binary.write_bytes(b'modified concurrently')
                    artifact = {'schema_version': 1, 'repository': payload['repository'], 'rows': [],
                                'summary': runner.summarize([])}
                    (artifact_dir / 'result.json').write_text(json.dumps(artifact))
                return {'status': 'completed', 'exit_code': 0, 'stdout': '', 'stderr': '', 'seconds': 0}

            argv = ['run.py', '--manifest', str(manifest), '--checkout', directory, '--radar', str(binary),
                    '--output', str(output), '--image', 'sha256:' + 'a' * 64]
            with patch.object(sys, 'argv', argv), patch.object(runner.ev, 'run', side_effect=simulated_command), patch.object(runner.subprocess, 'run'):
                # Source checkout metadata is supplied independently of mocked Docker.
                with patch.object(runner.ev, 'git', return_value='trusted source provenance'):
                    self.assertEqual(runner.main(), 0)
            result = json.loads(output.read_text())
            self.assertEqual(result['provenance']['manifest_sha256'], expected[0])
            self.assertEqual(result['provenance']['radar_sha256'], expected[1])

    def test_no_ground_truth_never_produces_accuracy(self):
        row = {'id': 'unknown', 'modes': [{'mode': 'full', 'repeat': 0, 'verdict': 'pass'}]}
        summary = runner.summarize([row])['full']
        self.assertIsNone(summary['false_pass_rate']['value'])
        self.assertEqual(summary['unavailable_ground_truth'], 1)

    def test_false_pass_fail_and_block_have_distinct_denominators(self):
        rows = []
        for state, verdict in [('failed', 'pass'), ('passed', 'fail'), ('passed', 'blocked')]:
            rows.append({'ground_truth_usable': True, 'ground_truth_status': state,
                'integration_only': False, 'modes': [{'mode': 'full', 'repeat': 0, 'verdict': verdict}]})
        summary = runner.summarize(rows)['full']
        self.assertEqual(summary['false_pass_rate']['value'], 1)
        self.assertEqual(summary['false_fail_rate']['value'], .5)
        self.assertEqual(summary['false_block_rate']['value'], .5)
        self.assertIsNone(summary['integration_defect_detection']['value'])

    def test_worker_refuses_unprotected_host(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            manifest = Path(directory) / 'manifest.json'
            manifest.write_text('{"schema_version":1,"repository":{"name":"test","revision":"' +
                'a' * 40 + '"},"full_suite":[{"argv":["python3"]}],"cases":[{"id":"x"}]}')
            argv = ['run.py', '--worker', '--manifest', str(manifest), '--checkout', directory,
                    '--radar', '/tmp/radar', '--output', str(Path(directory) / 'out.json')]
            with patch.object(sys, 'argv', argv), patch.object(runner, 'evaluate_worker') as evaluate:
                with self.assertRaises(SystemExit):
                    runner.main()
                evaluate.assert_not_called()

    def test_individually_failing_mutation_is_not_integration_only(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            (repo / 'a.txt').write_text('original a')
            (repo / 'b.txt').write_text('original b')
            runner.ev.git(repo, 'init', '-q')
            runner.ev.git(repo, 'add', '.')
            runner.ev.git(repo, 'commit', '-qm', 'trusted harness regression')
            revision = runner.ev.git(repo, 'rev-parse', 'HEAD')
            manifest = {'repository': {'revision': revision}, 'full_suite': [{'argv': ['unused']}],
                        'cases': [{'id': 'individually-failing', 'agents': [
                            [{'path': 'a.txt', 'find': 'original a', 'replace': 'modified a'}],
                            [{'path': 'b.txt', 'find': 'original b', 'replace': 'modified b'}]]}]}
            states = ['passed', 'failed', 'passed', 'failed']
            suites = [{'status': state, 'seconds': 0, 'commands': [
                {'recognized_result': state, 'test_files': []}]} for state in states]
            measurement = {'status': 'completed', 'exit_code': 0,
                           'stdout': json.dumps({'gate': {'verdict': 'fail'}}), 'seconds': 0}
            with patch.object(runner, 'suite', side_effect=suites), patch.object(runner.ev, 'run', return_value=measurement):
                result = runner.evaluate_worker(manifest, repo, Path('/unused'), 1, 1)
            self.assertEqual(result['rows'][0]['ground_truth_status'], 'failed')
            self.assertFalse(result['rows'][0]['integration_only'])
            self.assertEqual(result['summary']['full']['integration_defect_detection']['denominator'], 0)

    def test_supplementary_oracle_disagreement_does_not_discard_full_suite_truth(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            (repo / 'source.txt').write_text('original')
            runner.ev.git(repo, 'init', '-q')
            runner.ev.git(repo, 'add', '.')
            runner.ev.git(repo, 'commit', '-qm', 'trusted harness regression')
            manifest = {'repository': {'revision': runner.ev.git(repo, 'rev-parse', 'HEAD')},
                'full_suite': [{'argv': ['grouped']}], 'file_oracle': [{'argv': ['per-file'], 'test_files': ['one.py']}],
                'cases': [{'id': 'disagreement', 'agents': [[{'path': 'source.txt', 'find': 'original', 'replace': 'changed'}]]}]}
            states = ['passed', 'passed', 'passed', 'failed', 'passed']
            suites = [{'status': state, 'seconds': 0, 'commands': [
                {'recognized_result': state, 'test_files': ['one.py']}]} for state in states]
            measurement = {'status': 'completed', 'exit_code': 0, 'stdout': '{}', 'seconds': 0}
            with patch.object(runner, 'suite', side_effect=suites), patch.object(runner.ev, 'run', return_value=measurement):
                result = runner.evaluate_worker(manifest, repo, Path('/unused'), 1, 1)
            self.assertFalse(result['rows'][0]['combined_oracle_consistent'])
            self.assertTrue(result['rows'][0]['ground_truth_usable'])
            self.assertFalse(result['rows'][0]['test_file_oracle_available'])
            self.assertEqual(result['summary']['full']['observed_ground_truth'], 1)
            self.assertIsNone(result['summary']['full']['test_selection_recall']['value'])

    def test_supplementary_baseline_failure_preserves_integration_only_truth(self):
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            for filename in ('a.txt', 'b.txt'):
                (repo / filename).write_text('original')
            runner.ev.git(repo, 'init', '-q')
            runner.ev.git(repo, 'add', '.')
            runner.ev.git(repo, 'commit', '-qm', 'trusted harness regression')
            manifest = {'repository': {'revision': runner.ev.git(repo, 'rev-parse', 'HEAD')},
                'full_suite': [{'argv': ['native']}], 'file_oracle': [{'argv': ['per-file'], 'test_files': ['test.py']}],
                'cases': [{'id': 'integration', 'agents': [[{'path': filename, 'find': 'original', 'replace': 'changed'}]
                                                          for filename in ('a.txt', 'b.txt')]}]}
            states = ['passed', 'failed', 'passed', 'passed', 'failed', 'failed']
            suites = [{'status': state, 'seconds': 0, 'commands': [
                {'recognized_result': state, 'test_files': ['test.py']}]} for state in states]
            measurement = {'status': 'completed', 'exit_code': 0, 'stdout': '{}', 'seconds': 0}
            with patch.object(runner, 'suite', side_effect=suites), patch.object(runner.ev, 'run', return_value=measurement):
                result = runner.evaluate_worker(manifest, repo, Path('/unused'), 1, 1)
            row = result['rows'][0]
            self.assertTrue(row['ground_truth_usable'])
            self.assertTrue(row['integration_only'])
            self.assertFalse(row['test_file_oracle_available'])
            self.assertEqual(result['summary']['full']['integration_defect_detection']['denominator'], 1)
            self.assertIsNone(result['summary']['full']['test_selection_precision']['value'])
            # Native, consistent labels can support file metrics despite the
            # deliberately failing supplementary split-process baseline.
            for index in (0, 4):
                suites[index]['commands'][0]['junit'] = {'available': True,
                    'test_files': ['test.py'], 'failing_test_files': ['test.py'] if index == 4 else []}
            with patch.object(runner, 'suite', side_effect=suites), patch.object(runner.ev, 'run', return_value=measurement):
                labelled = runner.evaluate_worker(manifest, repo, Path('/unused'), 1, 1)['rows'][0]
            self.assertTrue(labelled['test_file_oracle_available'])
            self.assertEqual(labelled['file_oracle_source'], 'native_full_suite_junit')
            self.assertFalse(labelled['supplementary_file_oracle_available'])
            self.assertEqual(labelled['failing_test_files'], ['test.py'])


if __name__ == '__main__':
    unittest.main()
