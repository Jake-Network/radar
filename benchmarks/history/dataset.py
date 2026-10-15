#!/usr/bin/env python3
"""Build and score an independently labeled history corpus without repository execution.

export sources.json --out corpus.json: sources contains three entries with
language (go/typescript/python), repo (local clone), base; exports up to 20 cases
each. review corpus.json --out reviews.csv creates blank independent labels.
score corpus.json reviews.csv observations.json --out score.json validates
adjudication and reports per-cause recall, false positives and missed case IDs.
No candidate, follow-up heuristic, or tool result is a human label.
"""
import argparse
import csv
import hashlib
import json
import pathlib
import sys

import replay

CAUSES = tuple(c for c in replay.CAUSES if c != 'none') + ('unique_key', 'contract', 'textual')
LABELS = ('broken', 'clean', 'unknown')
CHECK_CAUSES = {'no_symbol_conflicts': 'symbol', 'migration_order': 'migration',
                'lockfile_consistency': 'lockfile', 'generated_drift': 'generated',
                'unique_keys': 'unique_key', 'no_breaking_contracts': 'contract',
                'textual_merge': 'textual'}
REVIEW_FIELDS = ('id', 'reviewer_1', 'label_1', 'causes_1', 'evidence_1',
                 'reviewer_2', 'label_2', 'causes_2', 'evidence_2',
                 'adjudicator', 'label', 'causes', 'evidence')


def digest(data):
    return hashlib.sha256(json.dumps(data, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def export(sources, per_repo=20):
    if per_repo < 1 or per_repo > 20:
        raise ValueError('per-repo must be between 1 and 20 (60-case total bound)')
    if len(sources) != 3 or {s['language'] for s in sources} != {'go', 'typescript', 'python'}:
        raise ValueError('provide exactly three local sources: go, typescript, python')
    repositories, cases = [], []
    for source in sorted(sources, key=lambda s: s['language']):
        mined = replay.mine(source['repo'], source['base'], source.get('since'), per_repo)
        repository = dict(mined['repository'], language=source['language'],
                          source_url=source.get('url'),
                          shallow=replay.git(source['repo'], 'rev-parse', '--is-shallow-repository').strip() == 'true')
        # Paths belong to local invocation configuration, not portable evidence.
        repositories.append(repository)
        for candidate in mined['candidates']:
            inputs = {'repository': repository['identity'], 'base': candidate['base'],
                      'branches': candidate['branches']}
            case = dict(candidate, merge_commit=candidate['id'], language=source['language'],
                        repository=repository['identity'], id=digest(inputs), input_digest=digest(inputs))
            case['trees'] = {ref: replay.git(source['repo'], 'rev-parse', ref + '^{tree}').strip()
                             for ref in [candidate['base'], *candidate['branches']]}
            cases.append(case)
    corpus = {'schema_version': 1, 'selection': 'latest parallel first-parent merges; not a random sample',
              'target_cases': 3 * per_repo, 'repositories': repositories, 'cases': cases}
    corpus['dataset_digest'] = digest(corpus)
    validate_corpus(corpus)
    return corpus


def validate_corpus(corpus):
    if corpus.get('schema_version') != 1 or corpus.get('dataset_digest') != digest({k: v for k, v in corpus.items() if k != 'dataset_digest'}):
        raise ValueError('corpus digest/schema mismatch')
    cases = corpus['cases']
    if len({c['id'] for c in cases}) != len(cases):
        raise ValueError('duplicate corpus case')
    for case in cases:
        inputs = {'repository': case['repository'], 'base': case['base'], 'branches': case['branches']}
        if case['input_digest'] != digest(inputs) or case['id'] != digest(inputs):
            raise ValueError('candidate input digest mismatch')
    return {c['id']: c for c in cases}


def review_template(corpus, path):
    validate_corpus(corpus)
    with open(path, 'w', newline='', encoding='utf-8') as stream:
        writer = csv.DictWriter(stream, fieldnames=REVIEW_FIELDS, lineterminator='\n')
        writer.writeheader()
        writer.writerows({'id': c['id']} for c in corpus['cases'])


def parse_label(label, causes, evidence):
    found = set(filter(None, (c.strip() for c in causes.split(';'))))
    if label not in LABELS or not found.issubset(CAUSES):
        raise ValueError('invalid human label or cause')
    if (label == 'broken') != bool(found):
        raise ValueError('broken requires causes; clean/unknown must have no causes')
    if not evidence.strip():
        raise ValueError('human review requires evidence or an explicit unknown reason')
    return label, tuple(sorted(found))


def reviews(corpus, path):
    cases, labels = validate_corpus(corpus), {}
    with open(path, newline='', encoding='utf-8') as stream:
        reader = csv.DictReader(stream)
        if not set(REVIEW_FIELDS).issubset(reader.fieldnames or []):
            raise ValueError('review CSV is missing required columns')
        for row in reader:
            row = {key: (value or '').strip() for key, value in row.items() if key is not None}
            case_id = row['id']
            if case_id not in cases or case_id in labels:
                raise ValueError('unknown or duplicate review case: ' + case_id)
            # A blank template is explicitly unlabeled, never a clean case.
            if not any(row.get(field, '').strip() for field in REVIEW_FIELDS[1:]):
                labels[case_id] = ('unlabeled', ())
                continue
            if not row['reviewer_1'].strip() or not row['reviewer_2'].strip() or row['reviewer_1'].strip() == row['reviewer_2'].strip():
                raise ValueError('two distinct human reviewers are required')
            first = parse_label(row['label_1'], row['causes_1'], row['evidence_1'])
            second = parse_label(row['label_2'], row['causes_2'], row['evidence_2'])
            if first == second and not row['label'].strip():
                labels[case_id] = first
            else:
                if not row['adjudicator'].strip():
                    raise ValueError('disagreement or override requires an adjudicator')
                labels[case_id] = parse_label(row['label'], row['causes'], row['evidence'])
    return labels


def radar_observations(corpus, runs):
    """Convert replay results, retaining missing/unavailable rows explicitly.

    Only exact base/branch inputs, repository identity, and a recorded binary
    digest qualify. Legacy replay results without inputs must be replayed.
    """
    cases = validate_corpus(corpus)
    observations = []
    for result in runs:
        if not result.get('radar_sha256'):
            raise ValueError('Radar replay requires binary digest')
        for row in result['results']:
            matching = [c for c in cases.values() if c['merge_commit'] == row['id'] and
                        c['repository'] == result['repository']['identity']]
            if len(matching) != 1:
                raise ValueError('replay does not identify one corpus case')
            case = matching[0]
            if row.get('base') != case['base'] or row.get('branches') != case['branches']:
                raise ValueError('replay inputs do not match corpus; rerun with current replay.py')
            detected = sorted({CHECK_CAUSES[k] for k, v in row['checks'].items()
                               if v == 'failed' and k in CHECK_CAUSES})
            observations.append({'id': case['id'], 'input_digest': case['input_digest'],
                                 'status': 'available' if row['verdict'] in ('pass', 'fail') else 'unavailable',
                                 'detected_causes': detected,
                                 'evidence': {'binary_sha256': result['radar_sha256'],
                                              'version': result.get('radar_version', 'unknown'),
                                              'scope': 'static; no repository code executed',
                                              'verdict': row['verdict'], 'checks': row['checks'],
                                              'finding_codes': row.get('problems', []),
                                              'seconds': row.get('seconds')}})
    return {'schema_version': 1, 'dataset_digest': corpus['dataset_digest'], 'tool': 'radar', 'observations': observations}


def score(corpus, labels, observation_sets):
    cases = validate_corpus(corpus)
    output = {'schema_version': 1, 'dataset_digest': corpus['dataset_digest'],
              'cases': len(cases), 'target_cases': corpus['target_cases'],
              'label_counts': {name: sum(labels.get(i, ('unlabeled', ()))[0] == name for i in cases)
                               for name in (*LABELS, 'unlabeled')}, 'tools': {}}
    for observations in observation_sets:
        tool = observations['tool']
        if observations.get('schema_version') != 1 or observations.get('dataset_digest') != corpus['dataset_digest'] or tool in output['tools']:
            raise ValueError('comparator dataset/schema mismatch or duplicate tool')
        rows = {}
        for row in observations['observations']:
            case_id = row['id']
            if case_id not in cases or case_id in rows or row.get('input_digest') != cases[case_id]['input_digest']:
                raise ValueError('comparator contains duplicate, unknown or mismatched candidate')
            if row.get('status') not in ('available', 'unavailable') or not isinstance(row.get('detected_causes'), list) or not set(row['detected_causes']).issubset(CAUSES):
                raise ValueError('invalid comparator status/causes')
            if not isinstance(row.get('evidence'), dict) or not row['evidence'] or (row['status'] == 'unavailable' and row['detected_causes']):
                raise ValueError('comparator needs evidence; unavailable cannot claim detections')
            rows[case_id] = row
        metrics = {}
        for cause in CAUSES:
            positives = [i for i in cases if cause in labels.get(i, ('unlabeled', ()))[1]]
            usable = [i for i in positives if rows.get(i, {}).get('status') == 'available']
            hits = [i for i in usable if cause in rows[i]['detected_causes']]
            negatives = [i for i in cases if labels.get(i, ('unlabeled', ()))[0] in ('clean', 'broken')
                         and cause not in labels[i][1] and rows.get(i, {}).get('status') == 'available']
            false_detections = [i for i in negatives if cause in rows[i]['detected_causes']]
            metrics[cause] = {'labeled_positives': len(positives), 'available': len(usable),
                              'unavailable': len(positives) - len(usable), 'detected': len(hits),
                              'recall': len(hits) / len(usable) if usable else None,
                              'recall_scope': 'available adjudicated positives only',
                              'recall_lower_bound': len(hits) / len(positives) if positives else None,
                              'negative_available': len(negatives), 'false_positives': false_detections,
                              'missed': [i for i in usable if i not in hits]}
        clean = [i for i in cases if labels.get(i, ('unlabeled', ()))[0] == 'clean' and rows.get(i, {}).get('status') == 'available']
        false_positive = [i for i in clean if rows[i]['detected_causes']]
        output['tools'][tool] = {'causes': metrics, 'clean_available': len(clean),
                                 'false_positives': false_positive,
                                 'false_positive_rate': len(false_positive) / len(clean) if clean else None,
                                 'unavailable': [i for i in cases if rows.get(i, {}).get('status') != 'available']}
    return output


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    export_parser = commands.add_parser('export')
    export_parser.add_argument('sources')
    export_parser.add_argument('--per-repo', type=int, default=20)
    review_parser = commands.add_parser('review')
    review_parser.add_argument('corpus')
    convert_parser = commands.add_parser('radar-results')
    convert_parser.add_argument('corpus')
    convert_parser.add_argument('runs', nargs='+')
    replay_parser = commands.add_parser('replay')
    replay_parser.add_argument('corpus')
    replay_parser.add_argument('--language', choices=('go', 'typescript', 'python'), required=True)
    replay_parser.add_argument('--repo', required=True)
    replay_parser.add_argument('--radar', required=True)
    replay_parser.add_argument('--limit', type=int, default=20)
    replay_parser.add_argument('--offset', type=int, default=0)
    replay_parser.add_argument('--timeout', type=int, default=60)
    score_parser = commands.add_parser('score')
    score_parser.add_argument('corpus')
    score_parser.add_argument('reviews')
    score_parser.add_argument('observations', nargs='+')
    for child in (export_parser, review_parser, convert_parser, replay_parser, score_parser):
        child.add_argument('--out', required=True)
    args = parser.parse_args(argv)
    read = lambda path: json.loads(pathlib.Path(path).read_text())
    try:
        if args.command == 'export':
            result = export(read(args.sources), args.per_repo)
        elif args.command == 'review':
            review_template(read(args.corpus), args.out)
            return 0
        elif args.command == 'replay':
            corpus = read(args.corpus)
            validate_corpus(corpus)
            if args.limit < 1 or args.limit > 20 or args.offset < 0 or args.offset > 19 or args.timeout < 1:
                raise ValueError('replay limit must be 1..20, offset 0..19; timeout must be positive')
            repository, = [r for r in corpus['repositories'] if r['language'] == args.language]
            candidates = [dict(c, id=c['merge_commit']) for c in corpus['cases'] if c['language'] == args.language][args.offset:args.offset + args.limit]
            result = replay.replay({'repository': repository, 'candidates': candidates}, args.repo, args.radar, args.timeout)
        elif args.command == 'radar-results':
            result = radar_observations(read(args.corpus), [read(p) for p in args.runs])
        else:
            corpus = read(args.corpus)
            result = score(corpus, reviews(corpus, args.reviews), [read(p) for p in args.observations])
        replay.write(args.out, result)
        return 0
    except (ValueError, KeyError, OSError) as error:
        parser.exit(2, 'dataset.py: %s\n' % error)


if __name__ == '__main__':
    sys.exit(main())
