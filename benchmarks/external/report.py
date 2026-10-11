#!/usr/bin/env python3
"""Pair immutable Radar observations with independently revalidated native truth.

Never changes either input. A pair needs the same project revision, dependency
image and exact candidate tree. Original runtime timings remain original.
"""
import argparse
import copy
import gzip
import hashlib
import json
import re
from pathlib import Path
import run


def read(path):
    raw = Path(path).read_bytes()
    data = gzip.decompress(raw) if str(path).endswith('.gz') else raw
    return json.loads(data), hashlib.sha256(raw).hexdigest()


def valid_sha(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{40}", value) is not None


def integrity_valid(suite):
    integrity = suite.get("source_integrity", {})
    return (integrity.get("checked") is True and integrity.get("source_modified") is False
            and integrity.get("tracked_changes_before") == []
            and integrity.get("tracked_changes_after") == []
            and valid_sha(integrity.get("before_commit"))
            and integrity.get("before_commit") == integrity.get("after_commit"))


def pair(measurements, oracle):
    if not valid_sha(measurements['repository']['revision']):
        raise ValueError('invalid upstream revision')
    if measurements['repository']['revision'] != oracle['repository']['revision']:
        raise ValueError('upstream revision mismatch')
    if measurements['provenance']['image'] != oracle['provenance']['image']:
        raise ValueError('dependency image mismatch')
    if not oracle.get('baseline') or any(b['tests']['status'] != 'passed' or not integrity_valid(b['tests']) for b in oracle['baseline']):
        raise ValueError('oracle baseline lacks passing source-integrity evidence')
    truth = {r['id']: r for r in oracle['rows']}
    rows = []
    for measured in measurements['rows']:
        actual = truth.get(measured['id'])
        if not valid_sha(measured.get('combined_tree')):
            raise ValueError('combined tree unavailable')
        if not actual or measured.get('combined_tree') != actual.get('combined_tree'):
            raise ValueError('combined tree mismatch: ' + measured['id'])
        if not measured['branches'] or any(not valid_sha(b.get('tree')) for b in measured['branches']):
            raise ValueError('branch tree unavailable')
        if [b.get('tree') for b in measured['branches']] != [b.get('tree') for b in actual['branches']]:
            raise ValueError('branch tree mismatch: ' + measured['id'])
        observed = [actual.get('combined_tests', {})] + [b['tests'] for b in actual['branches']]
        if any(not integrity_valid(s) for s in observed):
            raise ValueError('oracle source integrity unavailable')
        row = copy.deepcopy(actual)
        row['modes'] = copy.deepcopy(measured['modes'])
        known = row.get('combined_tests', {}).get('commands', [])
        labels = [c.get('junit') for c in known]
        files = sorted({f for label in labels if label and label.get('available') for f in label['test_files']}) if row.get('test_file_oracle_available') and labels and all(label and label.get('available') for label in labels) else None
        for mode in row['modes']:
            mode['runner_status'] = mode.get('runner_status', mode.get('runtime', {}).get('status', 'unavailable'))
            if mode.get('report', {}).get('candidate_tree') != actual['combined_tree']:
                raise ValueError('Radar candidate tree mismatch')
            mode['candidate_tree_matches_ground_truth'] = True
            mode.update(run.execution_timing(mode.get('report'), mode['end_to_end_seconds']))
            mode.update(run.inventory_metrics(mode.get('report'), mode.get('selection_report'), files, row.get('file_oracle_source')))
        rows.append(row)
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--measurements', required=True, type=Path)
    parser.add_argument('--oracle', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    destinations = [args.output.resolve(), args.output.with_suffix('.md').resolve()]
    if any(p.resolve() in destinations for p in (args.measurements, args.oracle)):
        parser.error('output must not overwrite an input')
    measured, measured_hash = read(args.measurements)
    oracle, oracle_hash = read(args.oracle)
    rows = pair(measured, oracle)
    result = {'schema_version': 1, 'repository': measured['repository'], 'rows': rows,
              'execution_status': 'completed', 'execution_kind': 'paired_evidence',
              'provenance': {'measurements': str(args.measurements), 'measurements_sha256': measured_hash,
                             'oracle': str(args.oracle), 'oracle_sha256': oracle_hash},
              'summary': run.summarize(rows),
              'limitations': ['Runtime timing and original conventional baseline come from the measurement artifact.',
                             'Source-integrity revalidation is a separate execution; input artifacts remain immutable.',
                             'The pairs share source tree and image; host load and affinity may differ.']}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    args.output.with_suffix('.md').write_text(run.markdown(result))


if __name__ == '__main__':
    main()
