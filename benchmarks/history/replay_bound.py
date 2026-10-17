#!/usr/bin/env python3
"""Resume static Radar replay of independently Git-bound sampled cases.

Reads existing local acquisitions only; never fetches, installs, or runs upstream
code. The supplied Radar binary is copied and hashed once for the whole batch.
"""
import argparse
import concurrent.futures
import hashlib
import json
import pathlib
import shutil

import dataset
import replay
import sampling


def batch(corpus, local_inputs, binary, output, jobs=2, timeout=90):
    cases = dataset.validate_corpus(corpus)
    output = pathlib.Path(output)
    output.mkdir(parents=True, exist_ok=True)
    pinned = output / 'radar'
    source_digest = sampling.file_digest(binary)
    if pinned.exists() and sampling.file_digest(pinned) != source_digest:
        raise ValueError('output contains a different pinned Radar binary; choose a new output directory')
    if not pinned.exists():
        shutil.copy2(binary, pinned)
        if sampling.file_digest(pinned) != source_digest:
            pinned.unlink()
            raise ValueError('Radar binary changed while pinning; retry with a stable source')
    result_dir = output / 'runs'
    result_dir.mkdir(exist_ok=True)

    def one(case):
        path = result_dir / (case['id'] + '.json')
        if path.exists():
            previous = sampling.read_json(path)
            row, = previous['results']
            if previous['radar_sha256'] != source_digest or row['base'] != case['base'] or row['branches'] != case['branches']:
                raise ValueError('cached replay input/binary mismatch')
            return previous
        local = local_inputs[case['source'] + ':' + case['source_id']]
        repository = {'identity': case['repository'], 'base_sha': case['branches'][0]}
        result = replay.replay({'repository': repository, 'candidates': [dict(case, id=case['merge_commit'])]},
                               local['repo'], pinned, timeout)
        if result.get('radar_sha256') != source_digest:
            raise ValueError('replay binary digest changed during batch')
        replay.write(path, result)
        return result

    runs = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=jobs) as pool:
        for result in pool.map(one, cases.values()):
            runs.append(result)
            row, = result['results']
            print(row['id'], row['verdict'], row['seconds'], flush=True)
    observations = dataset.radar_observations(corpus, runs)
    replay.write(output / 'radar-observations.json', observations)
    return observations


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('corpus')
    parser.add_argument('local_inputs')
    parser.add_argument('--radar', required=True)
    parser.add_argument('--out', required=True)
    parser.add_argument('--jobs', type=int, default=2)
    parser.add_argument('--timeout', type=int, default=90)
    args = parser.parse_args(argv)
    if not 1 <= args.jobs <= 4 or args.timeout < 1:
        parser.error('jobs must be 1..4; timeout must be positive')
    try:
        batch(sampling.read_json(args.corpus), sampling.read_json(args.local_inputs), args.radar,
              args.out, args.jobs, args.timeout)
    except (ValueError, KeyError, OSError) as error:
        parser.exit(2, 'replay_bound.py: %s\n' % error)


if __name__ == '__main__':
    main()
