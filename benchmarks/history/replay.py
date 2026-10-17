#!/usr/bin/env python3
"""Replay real merges from a local clone through `radar gate` (static).

    replay.py mine REPO --base REF [--since DATE] [--limit N] [--window-hours H] --out candidates.json
    replay.py replay candidates.json --radar BIN --out results.json
    replay.py report results.json [--labels labels.csv] [--out table.md]

`mine` lists merges on the base's first-parent history. A merge M with parents
P1 (the base line) and P2 (the merged branch) is replayed as base =
merge-base(P1, P2) and branches [P1, P2]: everything that landed on the base
line meanwhile is one branch, the merged work the other. Squash merges are
replayed only when `refs/pull/N/head` for the "(#N)" in the subject is already
fetched. Commits within the window after M that touch files from both sides
(at least one changed on each) are recorded as follow-ups, the human signal that the combination broke.

Nothing is fetched, cloned, installed or executed except Git and the Radar
binary given; `radar gate` runs without --run, so repository code never runs.
Output is deterministic for the same clone, arguments and Radar binary, except
the recorded durations.
"""
import argparse
import csv
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import time

GIT_ENV = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
GIT_ENV.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_TERMINAL_PROMPT='0',
               GIT_NO_LAZY_FETCH='1', LC_ALL='C')
FIX = re.compile(r'\b(fix|fixes|fixed|revert|reverts|hotfix|broken|repair|unbreak)\b', re.I)
PR_NUMBER = re.compile(r'\(#([1-9][0-9]{0,8})\)\s*$')
DJANGO_MERGE = re.compile(r'(^|/)migrations/[0-9]+_merge[^/]*\.py$')
ALEMBIC_MERGE = re.compile(r'^\s*down_revision\s*(?::[^=]*)?=\s*[\(\[]', re.M)
ERROR_KINDS = ('unsupported_repository_layout', 'radar_error_response', 'invalid_json_response', 'timeout')
CAUSES = ('symbol', 'migration', 'lockfile', 'generated', 'route', 'runtime', 'other', 'none')


# The clone's own config is still read; these keep it from running programs.
SAFE = ['-c', 'log.showSignature=false', '-c', 'core.fsmonitor=false', '-c', 'diff.external=', '-c', 'core.pager=cat']


def git(repo, *args):
    return subprocess.run(['git', '-C', str(repo), *SAFE, *args], env=GIT_ENV, check=True,
                          capture_output=True, text=True).stdout


def git_ok(repo, *args):
    p = subprocess.run(['git', '-C', str(repo), *SAFE, *args], env=GIT_ENV, capture_output=True, text=True)
    return p.returncode == 0, p.stdout


def changed(repo, a, b):
    return sorted(x for x in git(repo, 'diff', '--name-only', '--no-renames', a, b).splitlines() if x)


def touched(repo, sha, added=False):
    """Files a first-parent commit changes, against its first parent, so a
    merge commit counts what it brought in."""
    parents = git(repo, 'rev-list', '--parents', '-n', '1', sha).split()[1:]
    filters = ['--diff-filter=A'] if added else []
    if parents:
        out = git(repo, 'diff', '--name-only', '--no-renames', *filters, parents[0], sha)
    else:
        out = git(repo, 'diff-tree', '--root', '-r', '--no-commit-id', '--name-only', '--no-renames', *filters, sha)
    return sorted(x for x in out.splitlines() if x)


def signals(repo, sha):
    """Migration merge files a commit adds: a divergence someone had to join."""
    out = []
    for path in touched(repo, sha, added=True):
        if DJANGO_MERGE.search(path):
            out.append({'kind': 'django_merge_migration', 'path': path})
        elif path.endswith('.py') and '/versions/' in '/' + path:
            ok, text = git_ok(repo, 'show', sha + ':' + path)
            if ok and ALEMBIC_MERGE.search(text):
                out.append({'kind': 'alembic_merge_revision', 'path': path})
    return out


def mine(repo, base, since=None, limit=200, window_hours=48):
    repo = pathlib.Path(repo).resolve()
    ok, _ = git_ok(repo, 'rev-parse', '--verify', '--quiet', base + '^{commit}')
    if not ok:
        raise ValueError('base %r is not a commit in %s' % (base, repo))
    args = ['rev-list', '--first-parent', '--format=%H %ct %P', base]
    if since:
        args.insert(1, '--since=' + since)
    lines = [l for l in git(repo, *args).splitlines() if l and not l.startswith('commit ')]
    history = []  # oldest first
    for line in reversed(lines):
        fields = line.split()
        history.append({'sha': fields[0], 'time': int(fields[1]), 'parents': fields[2:]})
    candidates, skipped = [], {'octopus': 0, 'not_parallel': 0, 'direct_commit': 0, 'squash_without_pr_head': 0, 'root': 0}
    for index, c in enumerate(history):
        subject = git(repo, 'log', '-1', '--format=%s', c['sha']).strip()
        if len(c['parents']) > 2:
            skipped['octopus'] += 1
            continue
        if not c['parents']:
            skipped['root'] += 1
            continue
        if len(c['parents']) == 2:
            kind, (line, merged) = 'merge', c['parents']
        else:
            number = PR_NUMBER.search(subject)
            if not number:
                skipped['direct_commit'] += 1
                continue
            ok, head = git_ok(repo, 'rev-parse', '--verify', '--quiet', 'refs/pull/%s/head^{commit}' % number.group(1))
            if not ok:
                skipped['squash_without_pr_head'] += 1
                continue
            kind, line, merged = 'squash', c['parents'][0], head.strip()
        ok, mb = git_ok(repo, 'merge-base', line, merged)
        mb = mb.strip()
        if not ok or mb in (line, merged):
            skipped['not_parallel'] += 1
            continue
        line_files, merged_files = changed(repo, mb, line), changed(repo, mb, merged)
        both = set(line_files) | set(merged_files)
        followups = []
        for later in history[index + 1:]:
            if later['time'] - c['time'] > window_hours * 3600:
                break
            files = set(touched(repo, later['sha']))
            touches_line, touches_merged = files & set(line_files), files & set(merged_files)
            if not (touches_line and touches_merged):
                continue
            later_subject = git(repo, 'log', '-1', '--format=%s', later['sha']).strip()
            followups.append({'sha': later['sha'], 'subject': later_subject, 'hours': round((later['time'] - c['time']) / 3600, 2),
                              'fix_words': bool(FIX.search(later_subject)), 'files': sorted(touches_line | touches_merged)[:20],
                              'signals': signals(repo, later['sha'])})
        own = signals(repo, c['sha'])
        candidates.append({'id': c['sha'], 'kind': kind, 'subject': subject, 'time': c['time'],
                           'base': mb, 'branches': [line, merged],
                           'line_changed': len(line_files), 'merged_changed': len(merged_files), 'touched': len(both),
                           'signals': own, 'followups': followups,
                           'suspect': bool(own) or any(f['fix_words'] or f['signals'] for f in followups)})
    candidates = candidates[-limit:] if limit else candidates
    root_commits = sorted(x for x in git(repo, 'rev-list', '--max-parents=0', base).split() if x)
    return {'schema_version': 1, 'repository': {'identity': root_commits, 'base': base, 'base_sha': git(repo, 'rev-parse', base).strip()},
            'parameters': {'since': since, 'limit': limit, 'window_hours': window_hours},
            'skipped': skipped, 'candidates': candidates}


def replay(candidates, repo, radar, timeout=300):
    repo = pathlib.Path(repo).resolve()
    # Candidate files are data: a forged branch such as --run must never become
    # an execution flag. Mining always emits full immutable commit hashes.
    commit_hash = re.compile(r'(?:[0-9a-f]{40}|[0-9a-f]{64})\Z')
    refs = [candidates['repository']['base_sha']]
    for candidate in candidates['candidates']:
        if len(candidate['branches']) != 2:
            raise ValueError('history replay requires exactly two pinned branches')
        refs.extend([candidate['base'], *candidate['branches']])
    if any(not isinstance(ref, str) or not commit_hash.fullmatch(ref) for ref in refs):
        raise ValueError('history replay inputs must be full immutable commit hashes')
    actual_roots = sorted(git(repo, 'rev-list', '--max-parents=0', candidates['repository']['base_sha']).split())
    if actual_roots != candidates['repository']['identity']:
        raise ValueError('replay clone identity does not match mined repository')
    rows = []
    with tempfile.TemporaryDirectory(prefix='radar-history-') as tmp:
        # Run a private copy so a rebuild mid-run cannot mix binaries.
        binary = pathlib.Path(tmp) / 'radar'
        shutil.copy2(radar, binary)
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        radar = binary
        version = subprocess.run([str(radar), 'version'], capture_output=True, text=True).stdout.strip()
        env = dict(GIT_ENV, NO_COLOR='1', RADAR_STATE_DIR=os.path.join(tmp, 'state'), RADAR_CONFIG_DIR=os.path.join(tmp, 'config'))
        for c in candidates['candidates']:
            argv = [str(radar), 'gate', '--json', '--root', str(repo), '--base', c['base'], *c['branches']]
            started = time.monotonic()
            try:
                p = subprocess.run(argv, env=env, capture_output=True, text=True, timeout=timeout)
                code, out, err = p.returncode, p.stdout, p.stderr
            except subprocess.TimeoutExpired:
                code, out, err = None, '', 'timeout'
            seconds = round(time.monotonic() - started, 3)
            row = {'id': c['id'], 'base': c['base'], 'branches': c['branches'],
                   'suspect': c['suspect'], 'exit_code': code, 'seconds': seconds}
            try:
                report = json.loads(out)
            except json.JSONDecodeError:
                report = None
            if not isinstance(report, dict) or 'gate' not in report:
                error_kind = 'timeout' if code is None else 'invalid_json_response'
                if isinstance(report, dict) and report.get('status') == 'error':
                    message = str(report.get('error', ''))
                    error_kind = ('unsupported_repository_layout' if 'preview refuses symlink or submodule:' in message
                                  else 'radar_error_response')
                row.update(verdict='error', error=error_kind, error_kind=error_kind,
                           response_digest=hashlib.sha256(out.encode()).hexdigest(), checks={}, problems=[])
            else:
                row.update(verdict=report['gate']['verdict'],
                           checks={x['id']: x['status'] for x in report['gate'].get('required_checks', [])},
                           problems=sorted({f['code'] for f in report.get('findings', []) if f.get('severity') == 'error'}))
            rows.append(row)
    return {'schema_version': 1, 'radar_version': version, 'radar_sha256': digest, 'repository': candidates['repository'], 'results': rows}


def load_labels(path):
    labels = {}
    if not path:
        return labels
    with open(path, newline='', encoding='utf-8') as f:
        for row in csv.DictReader(f):
            cause = (row.get('cause') or '').strip()
            if cause not in CAUSES:
                raise ValueError('unknown cause %r for %s; use one of %s' % (cause, row.get('id'), ', '.join(CAUSES)))
            case_id = row['id'].strip()
            if not case_id or case_id in labels:
                raise ValueError('empty or duplicate label id: %r' % case_id)
            labels[case_id] = cause
    return labels


def percentile(values, q):
    if not values:
        return None
    values = sorted(values)
    return values[min(len(values) - 1, int(round(q * (len(values) - 1))))]


def report(results, labels=None):
    labels = labels or {}
    groups = {}
    for r in results['results']:
        group = labels.get(r['id']) or ('unlabeled suspect' if r['suspect'] else 'unlabeled other')
        g = groups.setdefault(group, {'cases': 0, 'fail': 0, 'blocked': 0, 'pass': 0, 'error': 0, 'failing_checks': {}})
        g['cases'] += 1
        g[r['verdict'] if r['verdict'] in ('fail', 'blocked', 'pass') else 'error'] += 1
        for check, status in sorted(r['checks'].items()):
            if status == 'failed':
                g['failing_checks'][check] = g['failing_checks'].get(check, 0) + 1
    lines = ['| Cause | Cases | Radar FAIL | NOT VERIFIED | PASS | Error | Failing checks |', '| --- | --- | --- | --- | --- | --- | --- |']
    for name in sorted(groups):
        g = groups[name]
        checks = ', '.join('%s %d' % kv for kv in sorted(g['failing_checks'].items())) or '-'
        lines.append('| %s | %d | %d | %d | %d | %d | %s |' % (name, g['cases'], g['fail'], g['blocked'], g['pass'], g['error'], checks))
    seconds = [r['seconds'] for r in results['results'] if r['exit_code'] is not None]
    timing = 'Static gate seconds: p50 %s, p95 %s over %d runs (%s).' % (percentile(seconds, .5), percentile(seconds, .95), len(seconds), results.get('radar_version') or 'unknown version')
    false_fail = sum(1 for r in results['results'] if r['verdict'] == 'fail' and labels.get(r['id']) == 'none')
    return {'groups': groups, 'labeled_false_fail': false_fail, 'markdown': '\n'.join(lines) + '\n\n' + timing + '\n'}


def write(path, data):
    text = json.dumps(data, indent=2, sort_keys=True) + '\n' if not isinstance(data, str) else data
    if path:
        pathlib.Path(path).write_text(text, encoding='utf-8')
    else:
        sys.stdout.write(text)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    sub = parser.add_subparsers(dest='command', required=True)
    m = sub.add_parser('mine')
    m.add_argument('repo')
    m.add_argument('--base', required=True)
    m.add_argument('--since')
    m.add_argument('--limit', type=int, default=200)
    m.add_argument('--window-hours', type=float, default=48)
    m.add_argument('--out')
    r = sub.add_parser('replay')
    r.add_argument('candidates')
    r.add_argument('--repo', help='clone to replay in (default: the mined repository path)')
    r.add_argument('--radar', required=True)
    r.add_argument('--timeout', type=int, default=300)
    r.add_argument('--out')
    t = sub.add_parser('report')
    t.add_argument('results')
    t.add_argument('--labels', help='CSV with id,cause columns; cause is one of: ' + ', '.join(CAUSES))
    t.add_argument('--out')
    a = parser.parse_args(argv)
    try:
        return run_command(a)
    except (ValueError, OSError, subprocess.CalledProcessError) as err:
        detail = getattr(err, 'stderr', '') or ''
        sys.stderr.write('replay.py: %s %s\n' % (err, detail.strip()))
        return 2


def run_command(a):
    if a.command == 'mine':
        data = mine(a.repo, a.base, a.since, a.limit, a.window_hours)
        data['repository']['path'] = str(pathlib.Path(a.repo).resolve())
        write(a.out, data)
    elif a.command == 'replay':
        candidates = json.loads(pathlib.Path(a.candidates).read_text(encoding='utf-8'))
        write(a.out, replay(candidates, a.repo or candidates['repository']['path'], a.radar, a.timeout))
    else:
        results = json.loads(pathlib.Path(a.results).read_text(encoding='utf-8'))
        write(a.out, report(results, load_labels(a.labels))['markdown'])
    return 0


if __name__ == '__main__':
    sys.exit(main())
