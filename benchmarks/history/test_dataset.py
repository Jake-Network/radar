import csv
import json
from collections import Counter
import importlib.util
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import dataset


def corpus():
    cases = []
    for index in range(4):
        inputs = {'repository': ['root'], 'base': 'base', 'branches': ['a', str(index)]}
        key = dataset.digest(inputs)
        cases.append(dict(inputs, id=key, input_digest=key, merge_commit=str(index)))
    result = {'schema_version': 1, 'target_cases': 60, 'cases': cases}
    result['dataset_digest'] = dataset.digest(result)
    return result


class DatasetTests(unittest.TestCase):
    def test_metrics_do_not_treat_unknown_missing_or_unavailable_as_clean(self):
        data = corpus()
        a, b, c, d = [case['id'] for case in data['cases']]
        labels = {a: ('broken', ('migration',)), b: ('broken', ('migration',)),
                  c: ('clean', ()), d: ('unknown', ())}
        rows = [{'id': i, 'input_digest': i, 'status': 'available',
                 'detected_causes': causes, 'evidence': {'digest': 'binary'}}
                for i, causes in [(a, ['symbol']), (c, ['migration']), (d, ['symbol'])]]
        observed = {'schema_version': 1, 'tool': 'weave', 'dataset_digest': data['dataset_digest'], 'observations': rows}
        result = dataset.score(data, labels, [observed])
        migration = result['tools']['weave']['causes']['migration']
        self.assertEqual(migration['recall'], 0)
        self.assertEqual(migration['labeled_positives'], 2)
        self.assertEqual(migration['unavailable'], 1)
        self.assertEqual(migration['missed'], [a])
        self.assertEqual(result['tools']['weave']['false_positives'], [c])
        self.assertIsNone(result['tools']['weave']['causes']['route']['recall'])
        self.assertEqual(result['label_counts']['unknown'], 1)
        self.assertEqual(dataset.score(data, {}, [])['label_counts']['unlabeled'], 4)

    def test_rejects_modified_corpus_and_mismatched_comparator(self):
        data = corpus()
        observed = {'schema_version': 1, 'tool': 'git', 'dataset_digest': 'different', 'observations': []}
        with self.assertRaises(ValueError):
            dataset.score(data, {}, [observed])
        data['cases'][0]['base'] = 'changed'
        with self.assertRaises(ValueError):
            dataset.validate_corpus(data)

    def test_reviews_require_independent_reviews_and_adjudication(self):
        data = corpus()
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / 'reviews.csv'
            dataset.review_template(data, path)
            self.assertTrue(all(label[0] == 'unlabeled' for label in dataset.reviews(data, path).values()))
            row = dict.fromkeys(dataset.REVIEW_FIELDS, '')
            row.update(id=data['cases'][0]['id'], reviewer_1='alice', reviewer_2='bob',
                       label_1='broken', causes_1='migration', evidence_1='commit fix demonstrates divergent heads',
                       label_2='clean', evidence_2='no failure reproduced')
            def write():
                with path.open('w', newline='') as stream:
                    writer = csv.DictWriter(stream, fieldnames=dataset.REVIEW_FIELDS)
                    writer.writeheader()
                    writer.writerow(row)
            write()
            with self.assertRaises(ValueError):
                dataset.reviews(data, path)
            row.update(adjudicator='carol', label='unknown', evidence='cannot determine with available history')
            write()
            self.assertEqual(dataset.reviews(data, path)[row['id']], ('unknown', ()))
            row['reviewer_2'] = 'alice'
            write()
            with self.assertRaises(ValueError):
                dataset.reviews(data, path)

    def test_exact_radar_inputs_required_and_wrong_cause_is_not_recall(self):
        data = corpus()
        case = data['cases'][0]
        runs = [{'radar_sha256': 'digest', 'repository': {'identity': ['root']}, 'results': [
            {'id': case['merge_commit'], 'base': case['base'], 'branches': case['branches'],
             'verdict': 'fail', 'checks': {'no_symbol_conflicts': 'failed'}}]}]
        observed = dataset.radar_observations(data, runs)
        self.assertEqual(observed['observations'][0]['detected_causes'], ['symbol'])
        runs[0]['results'][0]['verdict'] = 'blocked'
        unavailable = dataset.radar_observations(data, runs)
        self.assertEqual(unavailable['observations'][0]['detected_causes'], [])
        self.assertEqual(unavailable['observations'][0]['status'], 'unavailable')
        runs[0]['results'][0].update(error_kind='unsupported_repository_layout', response_digest='a' * 64)
        explained = dataset.radar_observations(data, runs)
        self.assertEqual(explained['observations'][0]['evidence']['error_kind'], 'unsupported_repository_layout')
        runs[0]['results'][0]['error_kind'] = 'raw output is not an error kind'
        with self.assertRaisesRegex(ValueError, 'unsupported replay error kind'):
            dataset.radar_observations(data, runs)
        runs[0]['results'][0].pop('error_kind')
        runs[0]['results'][0]['response_digest'] = 'malformed'
        with self.assertRaisesRegex(ValueError, 'invalid replay response digest'):
            dataset.radar_observations(data, runs)
        dataset.score(data, {}, [unavailable])
        runs[0]['results'][0]['branches'] = ['wrong']
        with self.assertRaises(ValueError):
            dataset.radar_observations(data, runs)

    def test_checked_in_corpus_has_sixty_pinned_unlabeled_cases(self):
        path = HERE / 'corpus/candidates.json'
        data = json.loads(path.read_text())
        self.assertEqual(Counter(c['language'] for c in data['cases']),
                         {'go': 20, 'python': 20, 'typescript': 20})
        self.assertEqual(len(dataset.validate_corpus(data)), 60)
        labels = dataset.reviews(data, HERE / 'corpus/reviews.csv')
        self.assertEqual(len(labels), 60)
        self.assertTrue(all(label[0] == 'unlabeled' for label in labels.values()))
        self.assertNotIn('/tmp/', path.read_text())
        observations = json.loads((HERE / 'corpus/radar-observations.json').read_text())
        self.assertEqual(len(observations['observations']), 60)
        expected = dataset.score(data, labels, [observations])
        self.assertEqual(json.loads((HERE / 'corpus/score.json').read_text()), expected)
        self.assertTrue(all(metric['recall'] is None for metric in expected['tools']['radar']['causes'].values()))
        sources = json.loads((HERE / 'corpus/sources.json').read_text())
        self.assertTrue(all(s['url'].startswith('https://github.com/') and len(s['base']) == 40 for s in sources))

    def test_export_enforces_three_languages_and_60_case_bound(self):
        with self.assertRaises(ValueError):
            dataset.export([])
        sources = [{'language': lang, 'repo': '/unused', 'base': 'main'} for lang in ('go', 'typescript', 'python')]
        with self.assertRaises(ValueError):
            dataset.export(sources, 21)
        mined = {'repository': {'identity': ['root'], 'base': 'main', 'base_sha': 'sha'}, 'candidates': []}
        with patch.object(dataset.replay, 'mine', return_value=mined), patch.object(dataset.replay, 'git', return_value='false'):
            exported = dataset.export(sources)
        self.assertEqual(exported['target_cases'], 60)
        self.assertEqual(exported['cases'], [])
        dataset.validate_corpus(exported)


if __name__ == '__main__':
    unittest.main()
