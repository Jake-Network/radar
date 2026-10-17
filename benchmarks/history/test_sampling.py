import io
import json
import pathlib
import subprocess
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import dataset
import sampling


class SamplingTests(unittest.TestCase):
    def test_checked_in_400_requests_are_pinned_and_unverified(self):
        here = pathlib.Path(__file__).resolve().parent
        sampled = sampling.read_json(here / 'corpus/sample-400.json.gz')
        sampling.validate_sample(sampled)
        self.assertEqual(sampled['sources'], sampling.read_json(sampling.LOCK))
        self.assertEqual(sampled['target_cases'], 400)
        self.assertEqual(len(sampled['requests']), 400)
        self.assertEqual(sampled['summary']['agenticflict']['eligible'], 29609)
        self.assertEqual(sampled['summary']['aidev']['eligible'], 71677)
        for source in ('agenticflict', 'aidev'):
            self.assertEqual(sum(r['source'] == source for r in sampled['requests']), 200)
        self.assertTrue(all(r['status'] == 'unavailable' for r in sampled['requests']))
        bound = sampling.bind(sampled, {})
        self.assertEqual(bound['cases'], [])
        self.assertEqual(len(bound['availability']), 400)
        tables = sampling.score_strata(bound, {}, [])['tables']
        self.assertEqual(tables['agenticflict']['input_unavailable'], 200)
        self.assertEqual(tables['aidev']['input_unavailable'], 200)

    def rows(self):
        return {'agenticflict': [dict(repo_full_name='owner/repo', pr_number=i,
                                     conflict_label='True', base_oid='a' * 40, head_oid='b' * 40)
                                for i in range(1, 6)],
                'aidev': [dict(html_url=f'https://github.com/owner/repo/pull/{i}') for i in range(1, 6)]}

    def test_seeded_selection_is_order_independent_and_not_a_label(self):
        rows = self.rows()
        first = sampling.sample(rows, [], seed=12, count=2)
        second = sampling.sample({s: list(reversed(v)) for s, v in rows.items()}, [], seed=12, count=2)
        self.assertEqual(first['requests'], second['requests'])
        self.assertEqual(len(first['requests']), 4)
        self.assertTrue(all(r['status'] == 'unavailable' for r in first['requests']))
        result = sampling.bind(first, {})
        self.assertEqual(result['target_cases'], 4)
        self.assertEqual(result['cases'], [])
        self.assertEqual(len(result['availability']), 4)
        self.assertEqual(dataset.score(result, {}, [])['label_counts']['clean'], 0)
        first['seed'] = 'changed'
        with self.assertRaisesRegex(ValueError, 'digest'):
            sampling.bind(first, {})

    def test_rejection_reasons_and_shortfall_are_explicit(self):
        rows = self.rows()
        rows['agenticflict'][0]['conflict_label'] = 'false'
        rows['agenticflict'][1]['base_oid'] = '--run'
        rows['aidev'].append({'html_url': 'https://example.com/o/r/pull/1'})
        rows['aidev'].append(rows['aidev'][0])
        result = sampling.sample(rows, [], count=200)
        self.assertEqual(result['target_cases'], 400)
        self.assertEqual(result['summary']['agenticflict']['selected'], 3)
        self.assertEqual(result['summary']['aidev']['rejection_counts'], {'invalid_pr_url': 1, 'duplicate_pr': 1})
        self.assertEqual(result['summary']['agenticflict']['rejection_counts'], {'not_textual_conflict': 1, 'missing_immutable_inputs': 1})

    def test_archive_checksum_member_and_no_extraction(self):
        with tempfile.TemporaryDirectory() as temp:
            archive = pathlib.Path(temp) / 'source.tar.gz'
            with tarfile.open(archive, 'w:gz') as stream:
                for name in ('../../escape', 'data/clean/pr.csv'):
                    content = b'pr_number\n1\n'
                    member = tarfile.TarInfo(name)
                    member.size = len(content)
                    stream.addfile(member, io.BytesIO(content))
            source = dict(file=archive.name, format='tar.csv', member='data/clean/pr.csv',
                          size=archive.stat().st_size, checksum='sha256:' + sampling.file_digest(archive))
            self.assertEqual(list(sampling.source_rows(archive, source)), [{'pr_number': '1'}])
            self.assertFalse((pathlib.Path(temp).parent.parent / 'escape').exists())
            archive.write_bytes(b'corrupt')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                list(sampling.source_rows(archive, source))

    def test_bind_verifies_conflict_and_clean_pair_without_running_drivers(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            def git(*args):
                return subprocess.check_output(['git', '-C', temp, *args], text=True).strip()
            git('init', '-b', 'main')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            (root / 'shared.txt').write_text('original\n')
            (root / '.gitattributes').write_text('*.txt merge=evil\n')
            git('add', '.')
            git('commit', '-m', 'base')
            base = git('rev-parse', 'HEAD')
            (root / 'shared.txt').write_text('left\n')
            git('commit', '-am', 'left')
            left = git('rev-parse', 'HEAD')
            git('checkout', '-b', 'right', base)
            (root / 'shared.txt').write_text('right\n')
            git('commit', '-am', 'right')
            right = git('rev-parse', 'HEAD')
            git('checkout', '-b', 'clean', base)
            (root / 'other.txt').write_text('clean\n')
            git('add', '.')
            git('commit', '-m', 'clean')
            clean = git('rev-parse', 'HEAD')
            marker = root / 'driver-ran'
            git('config', 'merge.evil.driver', f'touch {marker}')
            rows = {'agenticflict': [dict(repo_full_name='o/r', pr_number=1, conflict_label=True, base_oid=left, head_oid=right)],
                    'aidev': [dict(html_url='https://github.com/o/r/pull/2')]}
            sampled = sampling.sample(rows, [], count=1)
            inputs = {'agenticflict:o/r#1': {'repo': temp}, 'aidev:o/r#2': {'repo': temp, 'base_oid': left, 'head_oid': clean, 'anchor_evidence': 'pinned historical snapshot'}}
            bound = sampling.bind(sampled, inputs)
            self.assertEqual(len(bound['cases']), 2, bound['availability'])
            self.assertEqual([c['textual_oracle']['conflict'] for c in bound['cases']], [True, False])
            self.assertFalse(marker.exists())
            self.assertEqual(len(dataset.validate_corpus(bound)), 2)
            self.assertNotIn(temp, json.dumps(bound))
            tables = sampling.score_strata(bound, {}, [])['tables']
            self.assertEqual(tables['agenticflict']['cases'], 1)
            self.assertEqual(tables['aidev']['label_counts']['unlabeled'], 1)
            self.assertEqual(tables['aidev']['input_unavailable'], 0)
            observed = {'schema_version': 1, 'dataset_digest': bound['dataset_digest'], 'tool': 'radar',
                        'observations': [{'id': c['id'], 'input_digest': c['id'], 'status': 'available',
                                          'detected_causes': [], 'evidence': {'scope': 'fixture'}} for c in bound['cases']]}
            metrics = sampling.score_strata(bound, {}, [observed])['tables']
            self.assertEqual(metrics['agenticflict']['textual_oracle_metrics']['radar']['missed'], [bound['cases'][0]['id']])
            self.assertEqual(metrics['aidev']['textual_oracle_metrics']['radar']['negative_available'], 1)
            self.assertEqual(metrics['aidev']['label_counts']['unlabeled'], 1)
            wrong = {'schema_version': 1, 'dataset_digest': bound['dataset_digest'], 'tool': 'git',
                     'observations': [{'id': 'absent'}]}
            with self.assertRaises(ValueError):
                sampling.score_strata(bound, {}, [wrong])
            self.assertTrue(all(c['base'] == base for c in bound['cases']))
            inputs['aidev:o/r#2']['head_oid'] = right
            rejected = sampling.bind(sampled, inputs)
            self.assertEqual(len(rejected['cases']), 1)
            self.assertEqual(rejected['availability'][1]['reason'], 'textual_stratum_mismatch')
            inputs['aidev:o/r#2'].pop('anchor_evidence')
            self.assertEqual(sampling.bind(sampled, inputs)['availability'][1]['reason'], 'historical_anchor_evidence_missing')


if __name__ == '__main__':
    unittest.main()
