#!/usr/bin/env python3
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('replay', HERE / 'replay.py')
replay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(replay)


class HistoryFixture(unittest.TestCase):
    """A main line with one diverging Alembic merge followed by a fix, one
    clean merge, one squash merge with a fetched PR head and one without."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='radar-history-test-')
        self.addCleanup(self.tmp.cleanup)
        self.repo = pathlib.Path(self.tmp.name) / 'repo'
        self.repo.mkdir()
        self.clock = 1_000_000_000
        self.git('init', '-q', '-b', 'main')
        self.put('db/env.py', "print('env')\n")
        self.put('db/versions/r0.py', "revision = 'r0'\ndown_revision = None\n")
        self.put('app.py', 'x = 1\n')
        self.commit('baseline')
        # Diverging merge: both sides add a revision on r0.
        self.git('checkout', '-q', '-b', 'orders')
        self.put('db/versions/a1.py', "revision = 'a1'\ndown_revision = 'r0'\n")
        self.commit('orders')
        self.git('checkout', '-q', 'main')
        self.put('db/versions/b1.py', "revision = 'b1'\ndown_revision = 'r0'\n")
        self.commit('refunds')
        self.merge('orders')
        self.diverging = self.head()
        self.put('db/versions/m.py', "revision = 'm'\ndown_revision = ('a1', 'b1')\n")
        self.put('db/versions/a1.py', "revision = 'a1'\ndown_revision = 'r0'\n# joined\n")
        self.put('db/versions/b1.py', "revision = 'b1'\ndown_revision = 'r0'\n# joined\n")
        self.commit('Fix multiple heads after merge')
        # Clean merge touching unrelated files.
        self.git('checkout', '-q', '-b', 'docs')
        self.put('README.md', 'docs\n')
        self.commit('docs')
        self.git('checkout', '-q', 'main')
        self.put('app.py', 'x = 2\n')
        self.commit('app')
        self.merge('docs')
        self.clean = self.head()
        # Squash merge with a fetched PR head.
        self.git('checkout', '-q', '-b', 'pr7')
        self.put('feature.py', 'y = 1\n')
        self.commit('feature')
        pr_head = self.head()
        self.git('checkout', '-q', 'main')
        self.put('other.py', 'z = 1\n')
        self.commit('other')
        self.put('feature.py', 'y = 1\n')
        self.commit('Add feature (#7)')
        self.squash = self.head()
        self.git('update-ref', 'refs/pull/7/head', pr_head)
        self.put('more.py', 'w = 1\n')
        self.commit('More work (#8)')

    def git(self, *args):
        env = dict(os.environ, GIT_AUTHOR_NAME='t', GIT_AUTHOR_EMAIL='t@example.invalid', GIT_COMMITTER_NAME='t',
                   GIT_COMMITTER_EMAIL='t@example.invalid', GIT_AUTHOR_DATE='@%d +0000' % self.clock,
                   GIT_COMMITTER_DATE='@%d +0000' % self.clock, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM='1')
        return subprocess.check_output(['git', '-C', str(self.repo), '-c', 'core.hooksPath=' + os.devnull, *args], env=env, text=True)

    def put(self, path, text):
        p = self.repo / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def commit(self, message):
        self.clock += 3600
        self.git('add', '.')
        self.git('commit', '-qm', message)

    def merge(self, branch):
        self.clock += 3600
        self.git('merge', '-q', '--no-ff', '--no-edit', branch)

    def head(self):
        return self.git('rev-parse', 'HEAD').strip()

    def test_mine_pairs_merges_with_followups_deterministically(self):
        first = replay.mine(self.repo, 'main')
        self.assertEqual(first, replay.mine(self.repo, 'main'))
        by_id = {c['id']: c for c in first['candidates']}
        self.assertEqual(set(by_id), {self.diverging, self.clean, self.squash})
        diverging = by_id[self.diverging]
        self.assertEqual(diverging['kind'], 'merge')
        self.assertEqual(diverging['base'], self.git('merge-base', diverging['branches'][0], diverging['branches'][1]).strip())
        self.assertTrue(diverging['suspect'])
        self.assertEqual([f['subject'] for f in diverging['followups']], ['Fix multiple heads after merge'])
        self.assertEqual(diverging['followups'][0]['signals'], [{'kind': 'alembic_merge_revision', 'path': 'db/versions/m.py'}])
        self.assertFalse(by_id[self.clean]['suspect'])
        self.assertEqual(by_id[self.squash]['kind'], 'squash')
        self.assertEqual(first['skipped'], {'octopus': 0, 'not_parallel': 0, 'direct_commit': 4, 'squash_without_pr_head': 1, 'root': 1})
        self.assertEqual(len(replay.mine(self.repo, 'main', limit=1)['candidates']), 1)

    def test_followup_landing_as_a_merge_commit_is_recorded(self):
        repo = self.repo
        # Rebuild the diverging merge's follow-up as a merged fix branch.
        self.git('reset', '-q', '--hard', self.diverging)
        self.git('checkout', '-q', '-b', 'fix-branch')
        self.put('db/versions/m.py', "revision = 'm'\ndown_revision = ('a1', 'b1')\n")
        self.put('db/versions/a1.py', "revision = 'a1'\ndown_revision = 'r0'\n# joined\n")
        self.put('db/versions/b1.py', "revision = 'b1'\ndown_revision = 'r0'\n# joined\n")
        self.commit('fix broken migrations')
        self.git('checkout', '-q', 'main')
        self.merge('fix-branch')
        mined = replay.mine(repo, 'main')
        diverging = [c for c in mined['candidates'] if c['id'] == self.diverging][0]
        self.assertTrue(diverging['suspect'])
        self.assertEqual(diverging['followups'][0]['signals'], [{'kind': 'alembic_merge_revision', 'path': 'db/versions/m.py'}])

    def test_missing_base_is_a_clean_error(self):
        empty = pathlib.Path(self.tmp.name) / 'empty'
        empty.mkdir()
        subprocess.check_call(['git', 'init', '-q', str(empty)])
        with self.assertRaises(ValueError):
            replay.mine(empty, 'main')
        self.assertEqual(replay.main(['mine', str(empty), '--base', 'main']), 2)

    def test_followups_outside_the_window_are_ignored(self):
        mined = replay.mine(self.repo, 'main', window_hours=0.5)
        diverging = [c for c in mined['candidates'] if c['id'] == self.diverging][0]
        self.assertEqual(diverging['followups'], [])
        self.assertFalse(diverging['suspect'])

    def test_report_groups_labels_and_flags_false_failures(self):
        results = {'radar_version': 'radar test', 'results': [
            {'id': 'a', 'suspect': True, 'exit_code': 1, 'seconds': 1.0, 'verdict': 'fail', 'checks': {'migration_order': 'failed'}, 'problems': []},
            {'id': 'b', 'suspect': False, 'exit_code': 1, 'seconds': 2.0, 'verdict': 'fail', 'checks': {'migration_order': 'failed'}, 'problems': []},
            {'id': 'c', 'suspect': False, 'exit_code': 0, 'seconds': 3.0, 'verdict': 'pass', 'checks': {}, 'problems': []},
            {'id': 'd', 'suspect': True, 'exit_code': None, 'seconds': 9.0, 'verdict': 'error', 'checks': {}, 'problems': []},
        ]}
        out = replay.report(results, {'a': 'migration', 'b': 'none'})
        self.assertEqual(out['groups']['migration']['fail'], 1)
        self.assertEqual(out['labeled_false_fail'], 1)
        self.assertEqual(out['groups']['unlabeled other']['pass'], 1)
        self.assertIn('p50 2.0, p95 3.0 over 3 runs', out['markdown'])
        self.assertEqual(out, replay.report(results, {'a': 'migration', 'b': 'none'}))

    def test_labels_reject_unknown_causes(self):
        path = pathlib.Path(self.tmp.name) / 'labels.csv'
        path.write_text('id,cause\nabc,typo\n')
        with self.assertRaises(ValueError):
            replay.load_labels(path)

    @unittest.skipUnless(os.environ.get('RADAR_BENCHMARK_BIN'), 'set RADAR_BENCHMARK_BIN to replay through a Radar executable')
    def test_replay_runs_static_gate_on_each_candidate(self):
        before = self.git('status', '--porcelain') + self.git('for-each-ref')
        mined = replay.mine(self.repo, 'main')
        results = replay.replay(mined, self.repo, os.environ['RADAR_BENCHMARK_BIN'])
        by_id = {r['id']: r for r in results['results']}
        self.assertEqual(by_id[self.diverging]['verdict'], 'fail')
        self.assertEqual(by_id[self.diverging]['checks'].get('migration_order'), 'failed')
        self.assertIn('migration_multiple_heads', by_id[self.diverging]['problems'])
        self.assertEqual(by_id[self.clean]['verdict'], 'pass')
        self.assertEqual(by_id[self.squash]['verdict'], 'pass')
        self.assertEqual(self.git('status', '--porcelain') + self.git('for-each-ref'), before)
        again = replay.replay(mined, self.repo, os.environ['RADAR_BENCHMARK_BIN'])
        strip = lambda rs: [{k: v for k, v in r.items() if k != 'seconds'} for r in rs['results']]
        self.assertEqual(strip(results), strip(again))


if __name__ == '__main__':
    unittest.main()
