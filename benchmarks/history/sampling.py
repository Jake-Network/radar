#!/usr/bin/env python3
"""Acquire pinned source data, sample 400 requests, bind local immutable Git inputs.

No repository code is executed or fetched. A sampled PR is not a human label.
Parquet conversion optionally requires pyarrow in the caller's environment.
"""
import argparse
from collections import Counter
import csv
import hashlib
import codecs
import gzip
import json
import pathlib
import re
import subprocess
import tarfile
import tempfile
import urllib.request

import dataset
import replay

def read_json(path):
    path = pathlib.Path(path)
    if path.suffix == '.gz':
        with gzip.open(path, 'rt', encoding='utf-8') as stream:
            return json.load(stream)
    return json.loads(path.read_text())


LOCK = pathlib.Path(__file__).with_name('sampling-sources.json')
SHA = re.compile(r'[0-9a-f]{40}(?:[0-9a-f]{24})?\Z')
SLUG = re.compile(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z')


def file_digest(path, algorithm='sha256'):
    result = hashlib.new(algorithm)
    with open(path, 'rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def acquire(directory, sources):
    directory = pathlib.Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    for source in sources:
        destination = directory / source['file']
        if not destination.exists():
            # Atomic rename only after verification. Never unpack source archives.
            with tempfile.NamedTemporaryFile(dir=directory, delete=False) as stream:
                temporary = pathlib.Path(stream.name)
                try:
                    with urllib.request.urlopen(source['url'], timeout=60) as response:
                        while block := response.read(1024 * 1024):
                            stream.write(block)
                    stream.close()
                    verify_file(temporary, source)
                    temporary.replace(destination)
                finally:
                    temporary.unlink(missing_ok=True)
        verify_file(destination, source)


def verify_file(path, source):
    algorithm, expected = source['checksum'].split(':', 1)
    if pathlib.Path(path).stat().st_size != source['size'] or file_digest(path, algorithm) != expected:
        raise ValueError('source size/checksum mismatch: ' + source['file'])


def source_rows(path, source):
    verify_file(path, source)
    if source['format'] == 'tar.csv':
        found = False
        with tarfile.open(path, 'r|gz') as archive:
            for member in archive:
                if member.name != source['member']:
                    continue
                if found or not member.isfile() or member.size > 256 * 1024 * 1024:
                    raise ValueError('invalid or duplicate PR CSV archive member')
                found = True
                with archive.extractfile(member) as binary:
                    yield from csv.DictReader(codecs.getreader('utf-8-sig')(binary))
        if not found:
            raise ValueError('pinned PR CSV member is absent')
    elif source['format'] == 'parquet':
        try:
            import pyarrow.parquet as parquet
        except ImportError as error:
            raise ValueError('Parquet conversion needs optional pyarrow; no dependency is installed automatically') from error
        for batch in parquet.ParquetFile(path).iter_batches(batch_size=1024):
            yield from batch.to_pylist()
    else:
        raise ValueError('unsupported source format')


def normalize(row, source):
    if source == 'agenticflict':
        if str(row.get('conflict_label', '')).lower() not in ('true', '1'):
            raise ValueError('not_textual_conflict')
        slug, number = row.get('repo_full_name', ''), str(row.get('pr_number', ''))
        base, head = row.get('base_oid', ''), row.get('head_oid', '')
        if not SHA.fullmatch(base) or not SHA.fullmatch(head):
            raise ValueError('missing_immutable_inputs')
    else:
        # AIDev PR metadata does not guarantee historical merge anchor SHAs.
        url = str(row.get('html_url', ''))
        match = re.fullmatch(r'https://github.com/([^/]+/[^/]+)/pull/([1-9][0-9]*)', url)
        if not match:
            raise ValueError('invalid_pr_url')
        slug, number = match.groups()
        base, head = '', ''
    if not SLUG.fullmatch(slug) or not number.isdigit() or int(number) < 1:
        raise ValueError('invalid_repository_or_pr')
    return {'source': source, 'source_id': slug + '#' + str(int(number)),
            'url': 'https://github.com/' + slug + '/pull/' + str(int(number)),
            'repository_url': 'https://github.com/' + slug, 'base_oid': base, 'head_oid': head,
            'requested_stratum': 'textual_conflict' if source == 'agenticflict' else 'clean_merge_candidate',
            'status': 'unavailable', 'reason': 'local_git_verification_required' if base else 'historical_input_anchors_required'}


def sample(rows_by_source, sources, seed=20261016, count=200):
    if count < 1 or count > 200:
        raise ValueError('per-source count must be 1..200')
    selected, summaries = [], {}
    for source in ('agenticflict', 'aidev'):
        eligible, rejections, seen = [], [], set()
        for index, row in enumerate(rows_by_source[source], 1):
            try:
                item = normalize(row, source)
                if item['source_id'] in seen:
                    raise ValueError('duplicate_pr')
                seen.add(item['source_id'])
                item['sample_key'] = dataset.digest([str(seed), source, item['source_id']])
                eligible.append(item)
            except ValueError as error:
                rejections.append({'row': index, 'reason': str(error)})
        eligible.sort(key=lambda item: (item['sample_key'], item['source_id']))
        selected.extend(eligible[:count])
        summaries[source] = {'rows': len(eligible) + len(rejections), 'eligible': len(eligible),
                             'selected': min(count, len(eligible)), 'not_selected': max(0, len(eligible) - count),
                             'rejection_counts': dict(Counter(r['reason'] for r in rejections)), 'rejections': rejections}
    result = {'schema_version': 1, 'target_cases': 2 * count, 'seed': str(seed),
              'selection': 'ascending SHA256 JSON [seed,source,canonical PR id]; first per-source count; no replacement after Git verification',
              'sources': sources, 'summary': summaries, 'requests': selected}
    result['sample_digest'] = dataset.digest(result)
    return result


def validate_sample(sampled):
    if sampled.get('schema_version') != 1 or sampled.get('sample_digest') != dataset.digest({k: v for k, v in sampled.items() if k != 'sample_digest'}):
        raise ValueError('sample digest/schema mismatch')


def bind(sampled, local_inputs):
    """Only verified pairs enter the existing labeled corpus; omissions stay explicit.

    local_inputs maps SOURCE:owner/repo#N to repo, base_oid, head_oid. For AIDev,
    anchors also require an evidence description (historical snapshot reference).
    """
    validate_sample(sampled)
    cases, repositories, availability = [], {}, []
    for request in sampled['requests']:
        key = request['source'] + ':' + request['source_id']
        local = local_inputs.get(key)
        if not local:
            availability.append({'id': key, 'status': 'unavailable', 'reason': 'local_inputs_missing'})
            continue
        try:
            base, head = request['base_oid'], request['head_oid']
            if not base:
                base, head = local.get('base_oid', ''), local.get('head_oid', '')
                if not local.get('anchor_evidence', '').strip():
                    raise ValueError('historical_anchor_evidence_missing')
            if not SHA.fullmatch(base) or not SHA.fullmatch(head):
                raise ValueError('missing_immutable_inputs')
            repo = local['repo']
            if replay.git(repo, 'rev-parse', '--is-shallow-repository').strip() != 'false':
                raise ValueError('shallow_history')
            # Private bare Git config prevents local merge drivers, hooks, fsmonitor
            # and replacement refs in the supplied clone from executing programs.
            objects = replay.git(repo, 'rev-parse', '--path-format=absolute', '--git-path', 'objects').strip()
            with tempfile.TemporaryDirectory(prefix='radar-history-git-') as tmp:
                replay.git(tmp, 'init', '--bare', '--template=')
                (pathlib.Path(tmp) / 'objects/info/alternates').write_text(objects + '\n')
                for ref in (base, head):
                    if replay.git(tmp, 'rev-parse', '--verify', ref + '^{commit}').strip() != ref:
                        raise ValueError('input_is_not_commit')
                ancestor = replay.git(tmp, 'merge-base', base, head).strip()
                if not SHA.fullmatch(ancestor):
                    raise ValueError('merge_base_unavailable')
                roots = sorted(replay.git(tmp, 'rev-list', '--max-parents=0', base, head).split())
                roots = sorted(set(roots))
                command = ['git', '-C', tmp, *replay.SAFE, 'merge-tree', '--write-tree', base, head]
                merged = subprocess.run(command, env=replay.GIT_ENV, capture_output=True, timeout=60)
                if merged.returncode not in (0, 1) or not SHA.fullmatch(merged.stdout.splitlines()[0].decode() if merged.stdout else ''):
                    raise ValueError('git_probe_error')
                conflict = merged.returncode == 1
                if conflict != (request['source'] == 'agenticflict'):
                    raise ValueError('textual_stratum_mismatch')
                inputs = {'repository': roots, 'base': ancestor, 'branches': [base, head]}
                case_id = dataset.digest(inputs)
                trees = {ref: replay.git(tmp, 'rev-parse', ref + '^{tree}').strip() for ref in (ancestor, base, head)}
            if any(case['id'] == case_id for case in cases):
                raise ValueError('duplicate_input_tuple')
            cases.append(dict(inputs, id=case_id, input_digest=case_id, merge_commit=case_id,
                              trees=trees, suspect=False, source=request['source'], source_id=request['source_id'],
                              textual_oracle={'conflict': conflict, 'argv': ['git', *replay.SAFE, 'merge-tree', '--write-tree', base, head],
                                              'output_digest': hashlib.sha256(merged.stdout + merged.stderr).hexdigest()},
                              anchor_evidence=local.get('anchor_evidence')))
            repository_key = dataset.digest(roots)
            repositories[repository_key] = {'identity': roots, 'base_sha': base, 'source_url': request['repository_url'], 'shallow': False}
            availability.append({'id': key, 'status': 'available', 'case_id': case_id})
        except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
            # Do not persist subprocess stdout/stderr or local clone paths.
            reason = str(error) if isinstance(error, ValueError) else type(error).__name__
            availability.append({'id': key, 'status': 'unavailable', 'reason': reason})
    result = {'schema_version': 1, 'selection': sampled['selection'], 'target_cases': sampled['target_cases'],
              'sample_digest': sampled['sample_digest'], 'repositories': list(repositories.values()),
              'cases': cases, 'availability': availability,
              'strata': {source: {'sampled': sum(r['source'] == source for r in sampled['requests']),
                                 'available': sum(c['source'] == source for c in cases),
                                 'human_labels': 'unavailable until two independent human reviews'}
                         for source in ('agenticflict', 'aidev')}}
    result['dataset_digest'] = dataset.digest(result)
    dataset.validate_corpus(result)
    return result



def score_strata(corpus, labels, observations):
    dataset.validate_corpus(corpus)
    tables = {}
    for source in ('agenticflict', 'aidev'):
        subset = dict(corpus, cases=[case for case in corpus['cases'] if case['source'] == source])
        subset['target_cases'] = corpus['target_cases'] // 2
        subset.pop('dataset_digest')
        subset['dataset_digest'] = dataset.digest(subset)
        identifiers = {case['id'] for case in subset['cases']}
        filtered = []
        for observation in observations:
            if observation['dataset_digest'] != corpus['dataset_digest']:
                raise ValueError('comparator dataset mismatch')
            filtered.append(dict(observation, dataset_digest=subset['dataset_digest'],
                                 observations=[row for row in observation['observations'] if row['id'] in identifiers]))
        table = dataset.score(subset, labels, filtered)
        table['input_unavailable'] = subset['target_cases'] - len(identifiers)
        table['textual_oracle_metrics'] = {}
        for observation in filtered:
            rows = {row['id']: row for row in observation['observations']}
            available = [case for case in subset['cases'] if rows.get(case['id'], {}).get('status') == 'available']
            positives = [case['id'] for case in available if case['textual_oracle']['conflict']]
            negatives = [case['id'] for case in available if not case['textual_oracle']['conflict']]
            missed = [key for key in positives if 'textual' not in rows[key]['detected_causes']]
            false_positives = [key for key in negatives if 'textual' in rows[key]['detected_causes']]
            table['textual_oracle_metrics'][observation['tool']] = {
                'scope': 'Git textual merge only; no semantic labels',
                'available': len(available), 'unavailable': subset['target_cases'] - len(available),
                'positive_available': len(positives), 'negative_available': len(negatives),
                'detected': len(positives) - len(missed), 'missed': missed,
                'false_positives': false_positives}
        table['scope'] = 'PR versus pinned target branch; textual conflicts only' if source == 'agenticflict' else 'clean textual merge candidates; semantic truth requires human review'
        tables[source] = table
    # Validate all comparator rows before filtering so malformed extras cannot hide.
    dataset.score(corpus, labels, observations)
    return {'schema_version': 1, 'dataset_digest': corpus['dataset_digest'], 'tables': tables}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    download = commands.add_parser('acquire')
    download.add_argument('directory')
    sampling = commands.add_parser('sample')
    sampling.add_argument('directory')
    sampling.add_argument('--seed', default='20261016')
    sampling.add_argument('--per-source', type=int, default=200)
    sampling.add_argument('--out', required=True)
    binding = commands.add_parser('bind')
    binding.add_argument('sample')
    binding.add_argument('local_inputs')
    binding.add_argument('--out', required=True)
    rerun = commands.add_parser('replay')
    rerun.add_argument('corpus')
    rerun.add_argument('--case', required=True)
    rerun.add_argument('--repo', required=True)
    rerun.add_argument('--radar', required=True)
    rerun.add_argument('--timeout', type=int, default=60)
    rerun.add_argument('--out', required=True)
    scoring = commands.add_parser('score')
    scoring.add_argument('corpus')
    scoring.add_argument('reviews')
    scoring.add_argument('observations', nargs='*')
    scoring.add_argument('--out', required=True)
    args = parser.parse_args(argv)
    read = read_json
    try:
        sources = read(LOCK)
        if args.command == 'acquire':
            acquire(args.directory, sources)
        elif args.command == 'sample':
            rows = {source['id']: source_rows(pathlib.Path(args.directory) / source['file'], source) for source in sources}
            replay.write(args.out, sample(rows, sources, args.seed, args.per_source))
        elif args.command == 'score':
            corpus = read(args.corpus)
            replay.write(args.out, score_strata(corpus, dataset.reviews(corpus, args.reviews), [read(path) for path in args.observations]))
        elif args.command == 'replay':
            corpus = read(args.corpus)
            cases = dataset.validate_corpus(corpus)
            if args.case not in cases or args.timeout < 1:
                raise ValueError('case must exist and timeout must be positive')
            case = cases[args.case]
            repository = {'identity': case['repository'], 'base_sha': case['branches'][0]}
            replay.write(args.out, replay.replay({'repository': repository, 'candidates': [dict(case, id=case['merge_commit'])]}, args.repo, args.radar, args.timeout))
        else:
            replay.write(args.out, bind(read(args.sample), read(args.local_inputs)))
    except (ValueError, OSError, tarfile.TarError) as error:
        parser.exit(2, 'sampling.py: %s\n' % error)


if __name__ == '__main__':
    main()
