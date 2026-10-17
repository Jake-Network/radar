import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import acquire_git


class AcquisitionTests(unittest.TestCase):
    def request(self):
        return {'source': 'aidev', 'source_id': 'owner/repository#1',
                'repository_url': 'https://github.com/owner/repository',
                'url': 'https://github.com/owner/repository/pull/1'}

    def metadata(self):
        pr = {'merged': True, 'merged_at': '2025-01-02T00:00:00Z',
              'merge_commit_sha': 'c' * 40, 'head': {'sha': 'b' * 40},
              'base': {'repo': {'private': False}}}
        merge = {'sha': 'c' * 40, 'parents': [{'sha': 'a' * 40}, {'sha': 'b' * 40}]}
        return pr, merge

    def test_historical_parents_are_used_instead_of_current_target_tip(self):
        pr, merge = self.metadata()
        pr['base']['sha'] = 'd' * 40
        anchors = acquire_git.historical_anchors(self.request(), pr, merge)
        self.assertEqual((anchors['base_oid'], anchors['head_oid']), ('a' * 40, 'b' * 40))
        self.assertIn('c' * 40, anchors['anchor_evidence'])

    def test_squash_open_moved_head_and_wrong_commit_are_unavailable(self):
        for mutation, reason in [
            (lambda p, m: p.update(merged=False), 'pr_not_merged'),
            (lambda p, m: m.update(parents=m['parents'][:1]), 'historical_two_parent_anchor_unavailable'),
            (lambda p, m: p['head'].update(sha='d' * 40), 'historical_merge_head_mismatch'),
            (lambda p, m: m.update(sha='e' * 40), 'historical_merge_head_mismatch')]:
            pr, merge = self.metadata()
            mutation(pr, merge)
            with self.assertRaisesRegex(acquire_git.Unavailable, reason):
                acquire_git.historical_anchors(self.request(), pr, merge)

    def test_acquisition_rejects_non_public_data_and_keeps_explicit_failure(self):
        with tempfile.TemporaryDirectory() as temp:
            acquirer = acquire_git.Acquirer(temp)
            pr, _ = self.metadata()
            pr['base']['repo']['private'] = True
            with patch.object(acquire_git, 'public_api', return_value=(pr, 'digest')):
                row, inputs = acquirer.one(self.request())
            self.assertIsNone(inputs)
            self.assertEqual(row['status'], 'unavailable')
            self.assertEqual(row['reason'], 'repository_not_confirmed_public')
            self.assertNotIn(temp, str(row))
            request = self.request()
            request['repository_url'] = 'https://example.com/owner/repository'
            with patch.object(acquire_git, 'public_api') as api:
                row, _ = acquirer.one(request)
            api.assert_not_called()
            self.assertEqual(row['reason'], 'invalid_public_repository')

    def test_missing_objects_are_explicitly_fetched_before_checkout(self):
        with tempfile.TemporaryDirectory() as temp:
            acquirer = acquire_git.Acquirer(temp)
            pr, merge = self.metadata()
            calls, missing_calls = [], []
            def git(repo, *args, **kwargs):
                calls.append(args)
                if args[0] == 'init':
                    (repo / '.git').mkdir()
                if args[0] == 'fetch' and args[-1] == 'e' * 40 and '--refetch' not in args:
                    raise acquire_git.Unavailable('absent_promisor_delta_base')
                if args[0] == 'merge-base':
                    return 'f' * 40 + '\n'
                if args[0] == 'ls-tree':
                    return 'src/module.py\n'
                if args[0] == 'rev-list' and '--missing=print' in args:
                    missing_calls.append(True)
                    return '?' + 'e' * 40 + '\n' if len(missing_calls) == 1 else ''
                if args[0] == 'rev-list':
                    return '1' * 40 + '\n'
                if args[0] == 'rev-parse':
                    return '2' * 40 + '\n'
                return ''
            with patch.object(acquire_git, 'public_api', side_effect=[(pr, 'pr-digest'), (merge, 'merge-digest')]), patch.object(acquirer, 'git', side_effect=git), patch.object(acquirer, 'validate_cache'):
                row, inputs = acquirer.one(self.request())
            self.assertEqual(row['status'], 'acquired', row)
            self.assertEqual(row['roots'], ['1' * 40])
            self.assertEqual(inputs['base_oid'], 'a' * 40)
            self.assertEqual(row['hydrated_blob_objects'], 1)
            self.assertEqual(row['api_provenance']['merge_payload_sha256'], 'merge-digest')
            hydrate = next(i for i, args in enumerate(calls) if args[0] == 'fetch' and '--refetch' in args and args[-1] == 'e' * 40)
            checkout = next(i for i, args in enumerate(calls) if args[0] == 'checkout')
            self.assertLess(hydrate, checkout)
            for args in [args for args in calls if args[0] == 'fetch']:
                self.assertIn('--no-auto-maintenance', args)
                self.assertIn('--no-recurse-submodules', args)
                self.assertIn('--no-write-commit-graph', args)
            self.assertNotIn('push', [args[0] for args in calls])
            self.assertNotIn('run', [args[0] for args in calls])

    def test_cached_configuration_rejects_filters_includes_origin_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temp:
            repo = pathlib.Path(temp) / 'cache'
            (repo / '.git').mkdir(parents=True)
            config = repo / '.git/config'
            safe = '[core]\nrepositoryformatversion = 0\nbare = false\n[remote "origin"]\nurl = https://github.com/owner/repository\npromisor = true\n'
            acquirer = acquire_git.Acquirer(temp)
            config.write_text(safe)
            acquirer.validate_cache(repo, 'https://github.com/owner/repository')
            for extra in ('[filter "evil"]\nsmudge = run-me\n', '[include]\npath = /private/config\n', '[core]\naskpass = run-me\n'):
                config.write_text(safe + extra)
                with self.assertRaises(acquire_git.Unavailable):
                    acquirer.validate_cache(repo, 'https://github.com/owner/repository')
            config.write_text(safe)
            with self.assertRaisesRegex(acquire_git.Unavailable, 'origin_mismatch'):
                acquirer.validate_cache(repo, 'https://github.com/different/repository')
            config.rename(repo / '.git/real-config')
            config.symlink_to('real-config')
            with self.assertRaisesRegex(acquire_git.Unavailable, 'unsafe_repository_cache'):
                acquirer.validate_cache(repo, 'https://github.com/owner/repository')

    def test_git_metadata_symlinks_cannot_redirect_cache_writes(self):
        for relative in ('objects', 'refs', 'index', 'info'):
            with self.subTest(relative=relative), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                repo, outside = root / 'cache', root / 'outside'
                (repo / '.git').mkdir(parents=True)
                outside.mkdir()
                marker = outside / 'untouched'
                marker.write_text('original')
                config = repo / '.git/config'
                config.write_text('[core]\nrepositoryformatversion = 0\nbare = false\n[remote "origin"]\nurl = https://github.com/owner/repository\n')
                (repo / '.git' / relative).symlink_to(outside if relative != 'index' else marker)
                with self.assertRaisesRegex(acquire_git.Unavailable, 'unsafe_repository_cache'):
                    acquire_git.Acquirer(temp).validate_cache(repo, 'https://github.com/owner/repository')
                self.assertEqual(marker.read_text(), 'original')

    def test_fast_git_completion_still_checks_uncached_mutation_size(self):
        with tempfile.TemporaryDirectory() as temp:
            acquirer = acquire_git.Acquirer(temp, repo_bytes=1)
            with patch.object(acquire_git.subprocess, 'Popen') as popen, patch.object(acquire_git, 'byte_size', return_value=2):
                popen.return_value.poll.return_value = 0
                popen.return_value.returncode = 0
                with self.assertRaisesRegex(acquire_git.Unavailable, 'repository_disk_budget'):
                    acquirer.git(pathlib.Path(temp), 'checkout', '--detach', 'a' * 40)

    def test_disk_budget_stops_before_network(self):
        with tempfile.TemporaryDirectory() as temp:
            pathlib.Path(temp, 'budget').write_bytes(b'exhausted')
            acquirer = acquire_git.Acquirer(temp, total_bytes=1)
            request = dict(self.request(), source='agenticflict', base_oid='a' * 40, head_oid='b' * 40)
            with patch.object(acquirer, 'git') as git:
                row, inputs = acquirer.one(request)
            git.assert_not_called()
            self.assertIsNone(inputs)
            self.assertEqual(row['reason'], 'total_disk_budget')


if __name__ == '__main__':
    unittest.main()
