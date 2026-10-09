import importlib.util
import json
import os
import pathlib
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('radar_benchmark', HERE / 'evaluate.py')
evaluation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaluation)

class EvaluationTests(unittest.TestCase):
    def manifest(self):
        path = HERE / 'manifests/integration.json'
        return json.loads(path.read_text()), path

    def test_missing_checkout_preserves_all_samples(self):
        manifest, path = self.manifest()
        manifest['repository'] = {'local_checkout': '/definitely/missing', 'revision': 'a' * 40}
        report = evaluation.evaluate(manifest, path, pathlib.Path('/missing/radar'))
        self.assertEqual(len(report['rows']), 2)
        self.assertEqual(report['summary']['ground_truth_observed'], 0)
        self.assertTrue(all(r['classification'] == 'environment_unavailable' for r in report['rows']))

    def test_gate_json_error_preserves_partial_selection(self):
        manifest, path = self.manifest()
        manifest['cases'] = manifest['cases'][:1]
        with tempfile.TemporaryDirectory() as tmp:
            radar = pathlib.Path(tmp) / 'radar'
            radar.write_text('#!/usr/bin/env python3\nimport json,sys\nif "gate" in sys.argv:\n print(json.dumps({"error":"preview refuses symlink", "status":"error"}));sys.exit(2)\nprint(json.dumps({"selection":{"commands":[{"test_files":["test_checkout.py"]}],"inventory_test_files":1}}))\n')
            radar.chmod(0o755)
            report = evaluation.evaluate(manifest, path, radar)
        row = report['rows'][0]
        self.assertEqual(row['classification'], 'analysis_unsupported')
        self.assertEqual(row['analysis']['exit_code'], 2)
        self.assertEqual(row['selected_test_files'], ['test_checkout.py'])
        self.assertEqual(row['inventory_test_files'], 1)
        self.assertEqual(row['analysis_error'], 'preview refuses symlink')

    def test_missing_runner_is_environment_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            observations = evaluation.suite(pathlib.Path(tmp), [{'argv': ['radar-nonexistent-test-runner'], 'recognizer': 'unittest'}], 3, None)
        self.assertEqual(evaluation.suite_state(observations), 'environment_unavailable')

    def test_no_results_and_unsupported_recognizer(self):
        self.assertEqual(evaluation.suite_state([]), 'no_relevant_test_executed')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'OK', 'recognizer': 'custom'}), 'analysis_unsupported')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'Ran 0 tests', 'recognizer': 'unittest'}), 'ground_truth_unavailable')

    def test_output_limit_and_timeout_cannot_be_success(self):
        import sys
        with tempfile.TemporaryDirectory() as tmp:
            noisy = evaluation.run([sys.executable, '-c', 'import sys; sys.stdout.write("x" * 100000)'], tmp, max_output_bytes=1024)
            self.assertEqual(noisy['status'], 'output_limit')
            self.assertTrue(noisy['output_truncated'])
            self.assertLessEqual(len(noisy['stdout']) + len(noisy['stderr']), 1024)
            timeout = evaluation.run([sys.executable, '-c', 'import time; time.sleep(5)'], tmp, timeout=0.1)
            self.assertEqual(timeout['status'], 'timeout')

    def test_node_spec_results_and_go_environment_errors(self):
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'ℹ tests 3', 'recognizer': 'node'}), 'passed')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 1, 'stdout': 'FAIL pkg [setup failed]', 'recognizer': 'go'}), 'environment_unavailable')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 2, 'stdout': '1 error', 'recognizer': 'pytest'}), 'environment_unavailable')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'ok pkg [no tests to run]', 'recognizer': 'go'}), 'ground_truth_unavailable')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': '{"Action":"pass","Package":"example"}', 'recognizer': 'go'}), 'ground_truth_unavailable')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': '{"Action":"pass","Package":"example","Test":"TestActual"}', 'recognizer': 'go'}), 'passed')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'test result: ok. 0 passed; 0 failed; 0 ignored', 'recognizer': 'cargo'}), 'ground_truth_unavailable')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'test result: ok. 1 passed; 0 failed; 0 ignored', 'recognizer': 'cargo'}), 'passed')

    def test_paths_and_unknown_denominators(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ValueError):
                evaluation.confined(pathlib.Path(tmp), '../escape')
        self.assertIsNone(evaluation.metric(0, 0)['value'])
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 0, 'stdout': 'Tests 2 passed (2)', 'recognizer': 'vitest'}), 'passed')
        self.assertEqual(evaluation.recognize({'status': 'completed', 'exit_code': 1, 'stdout': 'Tests: 1 failed, 1 total', 'recognizer': 'jest'}), 'failed')

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'set RADAR_BENCHMARK_BIN for actual executable fixture validation')
    def test_executable_two_branch_candidate(self):
        manifest, path = self.manifest()
        report = evaluation.evaluate(manifest, path, pathlib.Path(os.environ['RADAR_BENCHMARK_BIN']), True)
        broken, clean = report['rows']
        self.assertEqual(broken['classification'], 'defect_detected')
        self.assertEqual(report['summary']['integration_defect_detection_rate']['denominator'], 1)
        self.assertEqual(broken['ground_truth_status'], 'failed')
        for branch in broken['individual_branches']:
            self.assertEqual(evaluation.suite_state(branch['tests']), 'passed')
        self.assertEqual(clean['ground_truth_status'], 'passed')
        self.assertEqual(broken['selected_test_files'], ['test_checkout.py'])
        self.assertEqual(report['summary']['known_failing_test_recall']['value'], 1)
        self.assertTrue(broken['recognized_executions'])

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'requires Radar executable')
    def test_single_agent_failure_is_not_integration_detection(self):
        manifest, path = self.manifest()
        manifest['cases'] = [{'id': 'single-agent-defect', 'agents': [[{'path': 'backend.py', 'find': 'unit_price": 1', 'replace': 'unit_price": 3'}]]}]
        report = evaluation.evaluate(manifest, path, pathlib.Path(os.environ['RADAR_BENCHMARK_BIN']), True)
        self.assertEqual(report['rows'][0]['ground_truth_status'], 'failed')
        self.assertEqual(evaluation.suite_state(report['rows'][0]['individual_branches'][0]['tests']), 'failed')
        self.assertEqual(report['summary']['integration_defect_detection_rate']['denominator'], 0)
        self.assertEqual(report['summary']['mutation_defect_detection_rate']['numerator'], 1)

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'requires Radar executable')
    def test_absent_ci_baseline_does_not_fabricate_comparison(self):
        manifest, path = self.manifest()
        manifest.pop('baseline_suite', None)
        manifest['cases'] = manifest['cases'][:1]
        report = evaluation.evaluate(manifest, path, pathlib.Path(os.environ['RADAR_BENCHMARK_BIN']), True)
        self.assertEqual(report['rows'][0]['ground_truth_status'], 'failed')
        self.assertIsNone(report['rows'][0]['baseline_wall_seconds'])
        self.assertEqual(report['summary']['baseline_full_suite_wall_seconds'], [])

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'requires Radar executable')
    def test_missing_runner_in_full_evaluation(self):
        manifest, path = self.manifest()
        manifest['full_suite'] = [{'argv': ['radar-nonexistent-runner'], 'recognizer': 'unittest', 'test_files': ['test_checkout.py']}]
        report = evaluation.evaluate(manifest, path, pathlib.Path(os.environ['RADAR_BENCHMARK_BIN']), True)
        self.assertTrue(all(r['classification'] == 'environment_unavailable' for r in report['rows']))
        self.assertEqual(report['summary']['ground_truth_observed'], 0)

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'requires Radar executable')
    def test_untrusted_external_checkout_cannot_authorize_execution(self):
        manifest, path = self.manifest()
        with tempfile.TemporaryDirectory() as tmp:
            import shutil
            shutil.copytree(HERE / 'fixtures/integration', pathlib.Path(tmp) / 'repo')
            repo = pathlib.Path(tmp) / 'repo'
            evaluation.git(repo, 'init', '-q')
            evaluation.git(repo, 'add', '.')
            evaluation.git(repo, 'commit', '-qm', 'external baseline')
            manifest['repository'] = {'local_checkout': str(repo), 'revision': evaluation.git(repo, 'rev-parse', 'HEAD')}
            report = evaluation.evaluate(manifest, path, pathlib.Path(os.environ['RADAR_BENCHMARK_BIN']), True)
        self.assertTrue(all(r['classification'] == 'ground_truth_unavailable' for r in report['rows']))
        self.assertFalse(report['baseline_tests'])
        self.assertTrue(all('runtime_gate' not in r for r in report['rows']))

if __name__ == '__main__':
    unittest.main()
