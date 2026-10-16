import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import dataset
import replay_bound
import sampling


class ReplayBoundTests(unittest.TestCase):
    def corpus(self):
        inputs = {'repository': ['a' * 40], 'base': 'b' * 40, 'branches': ['c' * 40, 'd' * 40]}
        key = dataset.digest(inputs)
        case = dict(inputs, id=key, input_digest=key, merge_commit=key,
                    source='agenticflict', source_id='o/r#1', suspect=False)
        corpus = {'schema_version': 1, 'target_cases': 400, 'cases': [case]}
        corpus['dataset_digest'] = dataset.digest(corpus)
        return corpus

    def test_copy_race_refuses_unexpected_binary_before_any_replay(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source = root / 'input'
            source.write_bytes(b'initial binary')
            def racing_copy(a, b):
                pathlib.Path(b).write_bytes(b'replaced binary')
            with patch.object(replay_bound.shutil, 'copy2', side_effect=racing_copy), patch.object(replay_bound.replay, 'replay') as run:
                with self.assertRaisesRegex(ValueError, 'changed while pinning'):
                    replay_bound.batch(self.corpus(), {}, source, root / 'out')
            run.assert_not_called()
            self.assertFalse((root / 'out/radar').exists())

    def test_actual_inner_replay_digest_must_match_batch_pin_before_caching(self):
        corpus = self.corpus()
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source = root / 'input'
            source.write_bytes(b'batch binary')
            with patch.object(replay_bound.replay, 'replay', return_value={'radar_sha256': 'different'}):
                with self.assertRaisesRegex(ValueError, 'changed during batch'):
                    replay_bound.batch(corpus, {'agenticflict:o/r#1': {'repo': '/local/clone'}}, source, root / 'out')
            self.assertEqual(list((root / 'out/runs').glob('*.json')), [])

    def test_static_batch_pins_binary_and_resumes_exact_case(self):
        corpus = self.corpus()
        case = corpus['cases'][0]
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            binary = root / 'radar-input'
            binary.write_bytes(b'binary')
            expected = {'schema_version': 1, 'radar_sha256': sampling.file_digest(binary),
                        'repository': {'identity': case['repository']}, 'results': [
                            {'id': case['id'], 'base': case['base'], 'branches': case['branches'],
                             'verdict': 'fail', 'checks': {'textual_merge': 'failed'}, 'seconds': 1}]}
            inputs = {'agenticflict:o/r#1': {'repo': '/local/clone'}}
            with patch.object(replay_bound.replay, 'replay', return_value=expected) as run:
                observed = replay_bound.batch(corpus, inputs, binary, root / 'out')
            self.assertEqual(run.call_count, 1)
            self.assertEqual(observed['observations'][0]['detected_causes'], ['textual'])
            with patch.object(replay_bound.replay, 'replay') as run:
                self.assertEqual(replay_bound.batch(corpus, inputs, binary, root / 'out'), observed)
            run.assert_not_called()
            path = root / 'out/runs' / (case['id'] + '.json')
            modified = json.loads(path.read_text())
            modified['results'][0]['branches'] = ['wrong']
            path.write_text(json.dumps(modified))
            with self.assertRaisesRegex(ValueError, 'input/binary mismatch'):
                replay_bound.batch(corpus, inputs, binary, root / 'out')
            binary.write_bytes(b'changed binary')
            with self.assertRaisesRegex(ValueError, 'different pinned Radar'):
                replay_bound.batch(corpus, inputs, binary, root / 'out')


if __name__ == '__main__':
    unittest.main()
