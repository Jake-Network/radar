import importlib.util
import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
spec = importlib.util.spec_from_file_location('publish_checks', pathlib.Path(__file__).with_name('publish_checks.py'))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)

BASE, HEAD, OTHER = 'a' * 40, 'b' * 40, 'c' * 40


class CheckRunTests(unittest.TestCase):
    def report(self, verdict='blocked'):
        return {'workflow': {'base': BASE, 'head': HEAD}, 'gate': {'verdict': verdict, 'required_checks': [
            {'id': 'integration_execution', 'status': 'passed' if verdict == 'pass' else 'unknown', 'explanation': 'selected scope'}]}, 'findings': []}

    def api(self, path, token, method='GET', body=None):
        self.calls.append((path, method, body))
        if method == 'POST':
            return {'id': 42}
        number = int(path.rsplit('/', 1)[1])
        return {'state': 'open', 'base': {'sha': BASE, 'repo': {'full_name': 'owner/repo'}},
                'head': {'sha': HEAD if number == 1 else OTHER, 'repo': {'full_name': 'owner/repo'}}}

    def setUp(self):
        self.calls = []

    def publish(self, report=None, **kw):
        return checks.publish('owner/repo', report or self.report(), 'token', 1, HEAD, BASE,
                              enabled=True, trusted=True, request=kw.pop('request', self.api), **kw)

    def test_requires_opt_in_and_trust(self):
        for enabled, trusted in [(False, True), (True, False)]:
            with self.assertRaises(ValueError):
                checks.publish('owner/repo', self.report(), 'token', 1, HEAD, BASE, enabled, trusted, self.api)
        self.assertEqual(self.calls, [])

    def test_restack_inputs_never_publish_success(self):
        for landing in ({'branches': [], 'restack_first': [{'name': 'stale'}]},
                        {'api': {'branches': [], 'restack_first': [{'name': 'stale'}]}}):
            report = self.report('pass')
            report['landing'] = landing
            body = checks.payload(report, HEAD)
            self.assertEqual(body['conclusion'], 'action_required')
            self.assertIn('require restack', body['output']['summary'])

    def test_conclusions_preserve_missing_evidence(self):
        for verdict, conclusion in [('pass', 'success'), ('fail', 'failure'), ('blocked', 'neutral'), ('error', 'action_required'), ('unknown', 'action_required')]:
            self.publish(self.report(verdict))
            body = self.calls[-1][2]
            self.assertEqual(body['head_sha'], HEAD)
            self.assertEqual(body['conclusion'], conclusion)
            self.assertIn('Not verified:', body['output']['summary'])

    def test_stale_or_fork_target_is_rejected_before_mutation(self):
        for key, value in [('sha', OTHER), ('repo', {'full_name': 'attacker/fork'})]:
            def request(path, token, method='GET', body=None):
                row = self.api(path, token, method, body)
                row['head'][key] = value
                return row
            with self.assertRaises(ValueError):
                self.publish(request=request)
        self.assertTrue(all(method == 'GET' for _, method, _ in self.calls))

    def test_mismatched_report_is_rejected(self):
        report = self.report()
        report['workflow']['head'] = OTHER
        with self.assertRaises(ValueError):
            self.publish(report)
        self.assertEqual(self.calls, [])

    def test_pair_results_attach_to_both_pinned_heads(self):
        report = {'base': BASE, 'pairs': [{'pair': [1, 2], 'heads': [HEAD, OTHER], 'verdict': 'fail', 'report': self.report('pass')}]}
        self.publish(report)
        bodies = [body for _, method, body in self.calls if method == 'POST']
        self.assertEqual([body['head_sha'] for body in bodies], [HEAD, OTHER])
        self.assertTrue(all(body['conclusion'] == 'failure' for body in bodies))

    def test_annotations_bounded_and_paths_validated(self):
        report = self.report()
        report['findings'] = [{'code': 'symbol_conflict', 'severity': 'error', 'explanation': 'one\n::notice::two',
                               'locations': [{'path': 'src/api.py', 'line': 3}]}] * 75
        body = checks.payload(report, HEAD)
        self.assertEqual(len(body['output']['annotations']), 50)
        self.assertEqual(body['output']['annotations'][0]['start_line'], 3)
        for path in ['../secret', '/tmp/file', 'a\nb', 'C:\\secret']:
            self.assertIsNone(checks.annotation({'locations': [{'path': path, 'line': 1}]}))

    def test_workspace_retains_static_scope(self):
        body = checks.payload({'verdict': 'blocked', 'repos': [], 'cross_repo': {'status': 'unknown'}, 'scope_summary': '0 links'}, HEAD)
        self.assertEqual(body['conclusion'], 'neutral')
        self.assertIn('runtime interoperability remains unverified', body['output']['summary'])

class WorkspaceEvidenceTests(unittest.TestCase):
    def report(self):
        def repo(name, commit):
            return {'id': name, 'base': BASE, 'branches': [{'ref': 'agent/work', 'commit': commit}],
                    'verdict': 'pass', 'dirty': [], 'report': {'base': BASE, 'inputs': [commit],
                    'candidate_commit': 'd' * 40, 'candidate_tree': 'e' * 40,
                    'gate': {'verdict': 'pass', 'policy': {'version': 1, 'require': ['textual_merge']},
                    'required_checks': [{'id': 'textual_merge', 'status': 'passed',
                    'evidence': 'verified_static', 'explanation': 'Merged pinned input.'}]}}}
        return {'version': 1, 'verdict': 'pass', 'repo_count': 2, 'branch_count': 2,
                'repos': [repo('api', HEAD), repo('client', OTHER)], 'only': [],
                'cross_repo': {'status': 'passed', 'reason': 'declared static link checked'},
                'links': [{'id': 'response', 'producer': 'api', 'consumer': 'client', 'status': 'passed',
                'cells': [{'producer': p, 'consumer': c, 'status': 'passed'} for p, c in
                          [('base', 'base'), ('candidate', 'base'), ('base', 'candidate'), ('candidate', 'candidate')]]}]}

    def test_legitimate_pinned_workspace_success(self):
        self.assertEqual(checks.payload(self.report(), HEAD)['conclusion'], 'success')

    def test_empty_malformed_or_missing_workspace_evidence_cannot_pass(self):
        reports = [{'repos': [], 'verdict': 'pass'}, {'repos': [None], 'verdict': 'pass'},
                   {'repos': [{}], 'verdict': 'pass'},
                   {'repos': [{'report': {'gate': {'verdict': 'blocked', 'required_checks': [
                       {'id': 'integration_execution', 'status': 'unknown'}]}}}],
                    'verdict': 'pass', 'cross_repo': {'status': 'unknown'}}]
        for mutate in [lambda r: r.pop('repo_count'), lambda r: r.update(excluded_repos=[{'id': 'missing'}]),
                       lambda r: r.update(only=['api']), lambda r: r['repos'][0]['report'].pop('candidate_tree'),
                       lambda r: r['repos'][0]['report'].update(inputs=[OTHER]),
                       lambda r: r['repos'][0]['report']['gate'].update(required_checks=[]),
                       lambda r: r['repos'][0]['report']['gate']['policy']['require'].append('test_selection'),
                       lambda r: r['repos'][0]['report']['gate']['required_checks'][0].update(status='unknown'),
                       lambda r: r['repos'][0]['report']['gate'].update(verdict='blocked'),
                       lambda r: r['repos'][0].update(error='missing checkout'),
                       lambda r: r['cross_repo'].update(status='not_checked'), lambda r: r.pop('links'),
                       lambda r: r['links'][0]['cells'][-1].update(status='incomplete'),
                       lambda r: r.update(cross_repo_pairs={'status': 'passed', 'complete': False, 'pairs': []})]:
            report = self.report()
            mutate(report)
            reports.append(report)
        for report in reports:
            with self.subTest(report=report):
                body = checks.payload(report, HEAD)
                self.assertNotEqual(body['conclusion'], 'success')
                self.assertIn('success withheld', body['output']['summary'])

    def test_descendant_repair_does_not_clear_unevaluated_ancestor(self):
        calls = []
        ancestor = 'd' * 40
        def request(path, token, method='GET', body=None):
            calls.append((method, body))
            if method == 'POST':
                return {'id': len(calls)}
            n = int(path.rsplit('/', 1)[1])
            return {'state': 'open', 'base': {'sha': BASE, 'repo': {'full_name': 'owner/repo'}},
                    'head': {'sha': {1: HEAD, 2: OTHER, 3: ancestor}[n], 'repo': {'full_name': 'owner/repo'}}}
        inner = {'gate': {'verdict': 'pass', 'required_checks': [{'id': 'textual_merge', 'status': 'passed'}]}}
        report = {'base': BASE, 'member_heads': {'3': ancestor}, 'pairs': [{'pair': [1, 2],
                  'heads': [HEAD, OTHER], 'members': [[1, 3], [2]], 'verdict': 'pass', 'report': inner}]}
        checks.publish('owner/repo', report, 'token', enabled=True, trusted=True, request=request)
        posted = [body for method, body in calls if method == 'POST']
        self.assertEqual([body['head_sha'] for body in posted], [HEAD, OTHER])
        self.assertNotIn(ancestor, [body['head_sha'] for body in posted])


if __name__ == '__main__':
    unittest.main()
