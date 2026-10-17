import collections
import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import dataset
import sampling


class AcquiredSnapshotTests(unittest.TestCase):
    def test_actual_acquisition_preserves_all_requests_and_pinned_inputs(self):
        here = pathlib.Path(__file__).resolve().parent
        original = sampling.read_json(here / 'corpus/sample-400.json.gz')
        directory = here / 'corpus/phase0-400'
        acquired = sampling.read_json(directory / 'acquisition.json.gz')
        corpus = sampling.read_json(directory / 'candidates.json')
        self.assertEqual(acquired['sample_digest'], original['sample_digest'])
        self.assertEqual(corpus['sample_digest'], original['sample_digest'])
        requests = {r['source'] + ':' + r['source_id']: r for r in original['requests']}
        self.assertEqual({r['id'] for r in acquired['requests']}, set(requests))
        self.assertEqual(acquired['attempted'], 400)
        self.assertEqual(acquired['acquired'], 193)
        self.assertEqual(len(acquired['requests']), 400)
        self.assertEqual(len(dataset.validate_corpus(corpus)), 105)
        self.assertEqual(collections.Counter(c['source'] for c in corpus['cases']), {'agenticflict': 77, 'aidev': 28})
        self.assertEqual(len(corpus['availability']), 400)
        self.assertEqual(collections.Counter(r.get('reason', r['status']) for r in corpus['availability']),
                         {'available': 105, 'local_inputs_missing': 207, 'textual_stratum_mismatch': 88})
        rows = {r['id']: r for r in acquired['requests']}
        for case in corpus['cases']:
            key = case['source'] + ':' + case['source_id']
            record = rows[key]
            self.assertEqual(record['status'], 'acquired')
            self.assertEqual(case['branches'], [record['anchors']['base_oid'], record['anchors']['head_oid']])
            self.assertEqual(case['repository'], record['roots'])
            self.assertTrue(case['repository'])
            self.assertEqual(set(case['trees']), {case['base'], *case['branches']})
            self.assertTrue(all(sampling.SHA.fullmatch(sha) for sha in [case['base'], *case['branches'], *case['repository'], *case['trees'].values()]))
            self.assertEqual(case['textual_oracle']['conflict'], case['source'] == 'agenticflict')
            if case['source'] == 'agenticflict':
                self.assertEqual(case['branches'], [requests[key]['base_oid'], requests[key]['head_oid']])
            else:
                self.assertEqual(record['anchors']['method'], 'github_two_parent_merge')
                self.assertEqual(len(record['api_provenance']['merge_payload_sha256']), 64)
        labels = dataset.reviews(corpus, directory / 'reviews.csv')
        self.assertEqual(len(labels), 105)
        self.assertTrue(all(label[0] in (*dataset.LABELS, 'unlabeled') for label in labels.values()))
        self.assertNotIn('/tmp/', (directory / 'candidates.json').read_text())
        tables = sampling.score_strata(corpus, labels, [])['tables']
        self.assertEqual(tables['agenticflict']['input_unavailable'], 123)
        self.assertEqual(tables['aidev']['input_unavailable'], 172)
        self.assertEqual(sum(tables['aidev']['label_counts'].values()), 28)


    def test_expanded_version_preserves_previous_inputs_and_separates_additions(self):
        here = pathlib.Path(__file__).resolve().parent
        previous = sampling.read_json(here / 'corpus/phase0-400/candidates.json')
        directory = here / 'corpus/phase0-400-v2'
        corpus = sampling.read_json(directory / 'candidates.json')
        acquired = sampling.read_json(directory / 'acquisition.json.gz')
        manifest = sampling.read_json(directory / 'version.json')
        old, new = dataset.validate_corpus(previous), dataset.validate_corpus(corpus)
        self.assertEqual(len(new), 127)
        self.assertEqual(collections.Counter(c['source'] for c in new.values()), {'agenticflict': 83, 'aidev': 44})
        self.assertTrue(set(old).issubset(new))
        self.assertTrue(all(old[key]['input_digest'] == new[key]['input_digest'] for key in old))
        self.assertEqual(manifest['previous_dataset_digest'], previous['dataset_digest'])
        self.assertEqual(manifest['dataset_digest'], corpus['dataset_digest'])
        self.assertEqual(manifest['preserved_case_count'], 105)
        self.assertEqual(set(manifest['added_case_ids']), set(new) - set(old))
        self.assertEqual(len(manifest['added_case_ids']), 22)
        self.assertEqual(acquired['attempted'], 400)
        self.assertEqual(acquired['acquired'], 217)
        self.assertEqual(acquired['sample_digest'], corpus['sample_digest'])
        self.assertEqual(collections.Counter(r.get('reason', r['status']) for r in corpus['availability']),
                         {'available': 127, 'local_inputs_missing': 183, 'textual_stratum_mismatch': 90})
        labels = dataset.reviews(corpus, directory / 'reviews.csv')
        additions = dataset.reviews(corpus, directory / 'additional-reviews.csv')
        self.assertEqual(len(labels), 127)
        self.assertEqual(set(additions), set(manifest['added_case_ids']))
        tables = sampling.score_strata(corpus, labels, [])['tables']
        self.assertEqual(tables['agenticflict']['input_unavailable'], 117)
        self.assertEqual(tables['aidev']['input_unavailable'], 156)
        self.assertNotIn('/tmp/', (directory / 'candidates.json').read_text())


if __name__ == '__main__':
    unittest.main()
