#!/usr/bin/env python3
"""Pinned external evaluation. All external execution is inside offline Docker.

Reuses the existing evaluator's safe Git, bounded subprocess, recognition and
metric utilities. This module never installs dependencies or fetches repositories.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import xml.etree.ElementTree as ET

BASE = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('radar_evaluation', BASE / 'evaluate.py')
ev = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ev)
MODES = ('targeted', 'balanced', 'full')


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def worker_affinity():
    record = {'requested_cpus': 2, 'applied': False, 'allowed_cpus': None, 'effective_cpus': None}
    if not hasattr(os, 'sched_getaffinity') or not hasattr(os, 'sched_setaffinity'):
        record['unavailable_reason'] = 'OS CPU affinity API unavailable.'
        return record
    allowed = sorted(os.sched_getaffinity(0))
    record['allowed_cpus'] = allowed
    if Path('/.dockerenv').exists() and Path('/harness/external/run.py').exists():
        selected = allowed[:2]
        os.sched_setaffinity(0, selected)
        record['applied'] = True
    else:
        record['unavailable_reason'] = 'Affinity is applied only inside the disposable worker container.'
    record['effective_cpus'] = sorted(os.sched_getaffinity(0))
    return record


def cache_tmpfs_options(paths):
    options = []
    for path in paths:
        if not isinstance(path, str) or not re.fullmatch(r'/opt/(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+', path) or any(part in ('.', '..') for part in path.split('/')):
            raise ValueError('Writable cache paths must be confined absolute /opt directories without traversal or mount-option separators')
        options.append('--tmpfs=' + path + ':rw,nosuid,nodev,noexec,size=128m,mode=1777')
    return options


def extract_snapshot(source, destination):
    """Use the standard data filter, with a restrictive older-Python fallback.

    Older interpreters cannot apply tarfile's data filter. Their fallback only
    admits directories and regular files; it never extracts links or devices.
    """
    if hasattr(tarfile, 'data_filter'):
        source.extractall(destination, filter='data')
        return
    members = source.getmembers()
    for member in members:
        relative = Path(member.name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError('archive path escapes snapshot: ' + member.name)
        ev.confined(destination, member.name)
        if not (member.isfile() or member.isdir()):
            raise ValueError('archive links/special files unsupported on this Python version: ' + member.name)
    # Copy explicitly instead of using the legacy extractall implementation,
    # which applies untrusted ownership and permission metadata.
    for member in members:
        path = ev.confined(destination, member.name)
        if member.isdir():
            path.mkdir(parents=True, exist_ok=True)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            with source.extractfile(member) as contents, path.open('wb') as output:
                shutil.copyfileobj(contents, output)
            path.chmod((member.mode & 0o777) | 0o600)


def load_manifest(path):
    manifest = json.loads(Path(path).read_text())
    if manifest.get('schema_version') != 1:
        raise ValueError('unsupported manifest schema')
    if not re.fullmatch('[0-9a-f]{40}', manifest['repository'].get('revision', '')):
        raise ValueError('external repository requires immutable full commit SHA')
    for command in manifest.get('full_suite', []) + manifest.get('file_oracle', []) + manifest.get('baseline_suite', []):
        if not command.get('argv') or not all(isinstance(a, str) for a in command['argv']):
            raise ValueError('suite commands require argv arrays')
    if not manifest.get('full_suite') or not manifest.get('cases'):
        raise ValueError('full_suite and cases are required')
    return manifest


def parsed(record):
    if record['status'] != 'completed':
        return None
    try:
        value = json.loads(record['stdout'])
        return value if isinstance(value, dict) else None
    except ValueError:
        return None


def dependency_diagnostic(output):
    # Match emitted diagnostic lines, never identifiers or quoted expected
    # strings in assertion bodies. Unknown output remains the shared
    # recognizer's responsibility rather than becoming an environment error.
    patterns = (
        r'^(?:E[ \t]+)?ModuleNotFoundError:[ \t]',
        r'^Error(?: \[ERR_MODULE_NOT_FOUND\])?: (?:Cannot find (?:module|package)\b|ENOENT:|spawn [^\n]+ ENOENT\b|getaddrinfo (?:EAI_AGAIN|ENOTFOUND)\b|connect ECONNREFUSED\b)',
        r'^npm (?:ERR!|error) code (?:ENOENT|EAI_AGAIN|ENOTFOUND|ECONNREFUSED)\b',
        r'^(?:/[^:\n]+/(?:ba|da|z)?sh|(?:ba|da|z)?sh): (?:(?:line )?\d+: )?[^:\n]+: (?:command )?not found\b',
        r'^curl: \(\d+\) Could not resolve\b',
        r'^(?:go: [^\n]*(?:checksum mismatch|missing go.sum entry)|verifying [^\n]+: checksum mismatch|[^:\n]+: missing go.sum entry for module providing package)',
    )
    return any(re.search(pattern, output, re.MULTILINE) for pattern in patterns)


def suite(repo, commands, timeout):
    source_checked = (repo / '.git').exists()
    before_head = ev.git(repo, 'rev-parse', 'HEAD') if source_checked else None
    before_paths = ev.git(repo, 'diff', '--no-ext-diff', '--no-textconv', '--name-only', 'HEAD', '--').splitlines() if source_checked else []
    records = []
    for command in commands:
        report_path = ev.confined(repo, command['junit_report']) if command.get('junit_report') else None
        if report_path is not None:
            if report_path.is_symlink():
                raise ValueError('JUnit report refuses symlink')
            report_path.parent.mkdir(parents=True, exist_ok=True)
            report_path.unlink(missing_ok=True)
        record = ev.suite(repo, [command], timeout, None)[0]
        if report_path is not None:
            record['junit'] = read_junit(report_path, repo, record)
        records.append(record)
    for record in records:
        output = record.get('stdout', '') + record.get('stderr', '')
        if record['status'] in ('timeout', 'output_limit'):
            record['recognized_result'] = record['status']
        elif record.get('exit_code') not in (None, 0) and dependency_diagnostic(output):
            record['recognized_result'] = 'environment_unavailable'
    status = next((state for state in ('timeout', 'output_limit')
                   if any(r['recognized_result'] == state for r in records)), ev.suite_state(records))
    after_head = ev.git(repo, 'rev-parse', 'HEAD') if source_checked else None
    after_paths = ev.git(repo, 'diff', '--no-ext-diff', '--no-textconv', '--name-only', 'HEAD', '--').splitlines() if source_checked else []
    source_modified = bool(before_paths or after_paths or before_head != after_head)
    if source_modified:
        status = 'source_modified'
    return {'status': status, 'commands': records,
            'source_integrity': {'checked': source_checked, 'before_commit': before_head,
                'after_commit': after_head, 'tracked_changes_before': before_paths,
                'tracked_changes_after': after_paths, 'source_modified': source_modified,
                'limitation': 'Untracked additions are not inspected; transient edits restored before the final check are not detected.'},
            'seconds': sum(r['seconds'] for r in records),
            'peak_process_rss_kib': max((r.get('peak_rss_kib') or 0 for r in records), default=0) or None}


def read_junit(path, repo, command):
    """Supplement native execution with independently emitted file labels."""
    unavailable = {'available': False, 'test_files': None, 'failing_test_files': None}
    try:
        if path.is_symlink() or not path.is_file():
            raise ValueError('JUnit report missing or symlink')
        if path.stat().st_size > 8 * 1024 * 1024:
            raise ValueError('JUnit report exceeds 8 MiB')
        with path.open('rb') as report:
            raw = report.read(8 * 1024 * 1024 + 1)
        if len(raw) > 8 * 1024 * 1024:
            raise ValueError('JUnit report exceeds 8 MiB')
        data = raw.decode('utf-8')
        if re.search(r'<!\s*(?:DOCTYPE|ENTITY)', data, re.I):
            raise ValueError('JUnit declarations are unsupported')
        root = ET.fromstring(data)
        cases = list(root.iter('testcase'))
        if not cases:
            raise ValueError('JUnit contains no test cases')
        files, failing = set(), set()
        for case in cases:
            filename = case.get('file')
            if not filename or Path(filename).is_absolute() or '..' in Path(filename).parts:
                raise ValueError('JUnit case lacks a repository-relative file binding')
            if not ev.confined(repo, filename).is_file():
                raise ValueError('JUnit file binding does not name a repository file')
            files.add(filename)
            if case.find('failure') is not None or case.find('error') is not None:
                failing.add(filename)
        state = 'failed' if failing else 'passed'
        if command['status'] != 'completed' or command['recognized_result'] != state:
            raise ValueError('JUnit and recognized native command outcomes disagree')
        return {'available': True, 'case_count': len(cases), 'status': state,
                'test_files': sorted(files), 'failing_test_files': sorted(failing),
                'sha256': hashlib.sha256(raw).hexdigest()}
    except (OSError, ValueError, UnicodeError, ET.ParseError) as error:
        return {**unavailable, 'unavailable_reason': str(error)}


def selected_files(report):
    selection = (report or {}).get('selection') or {}
    return sorted({p for c in selection.get('commands', []) for p in c.get('test_files', [])})


def execution_timing(report, elapsed):
    executions = (report or {}).get('executions', [])
    if not executions and (report or {}).get('execution'):
        executions = [report['execution']]
    selected = sum(e['duration_ms'] for e in executions) / 1000 if executions and all('duration_ms' in e for e in executions) else None
    attribution = any(item.get('runs') for item in (report or {}).get('observed_attributions', []))
    total = None if attribution else selected
    return {'selected_test_execution_seconds': selected, 'observed_execution_count': len(executions),
            'test_execution_seconds': total,
            'test_execution_unavailable_reason': 'Attribution rerun durations are not exposed by Radar.' if attribution else ('No execution durations observed.' if total is None else None),
            'non_execution_seconds': max(0, elapsed - total) if total is not None else None}


def inventory_metrics(report, selection_report, known_files, source=None):
    proposal = (report or {}).get('verification_proposal') or (selection_report or {}).get('verification_proposal')
    inventory = proposal.get('inventory') if isinstance(proposal, dict) else None
    tests = inventory.get('tests') if isinstance(inventory, dict) else None
    inventoried = sorted({test['path'] for test in tests if isinstance(test, dict) and isinstance(test.get('path'), str)}) if isinstance(tests, list) else None
    selection = (report or {}).get('selection') or (selection_report or {}).get('selection') or {}
    omissions = (proposal or {}).get('omitted', []) + selection.get('omitted', [])
    unsupported = sorted({path for omission in omissions if omission.get('reason') == 'unsupported_configuration'
                          for path in omission.get('test_files', [])}) if proposal is not None or selection else None
    runner_missing = sorted({path for command in (proposal or {}).get('commands', [])
                             if command.get('tool_available') is False for path in command.get('test_files', [])}) if proposal is not None else None
    reason = 'No independently executed per-file full-suite baseline is available.' if known_files is None else (
        'Radar did not provide an inventory with test paths.' if inventoried is None else None)
    return {'known_full_suite_test_files': known_files,
            'independent_inventory_basis': ('native full-suite JUnit testcase file paths' if source == 'native_full_suite_junit' else 'manifest per-file oracle list verified through independent per-file baseline commands') if known_files is not None else None,
            'inventoried_test_files': inventoried,
            'missing_inventory_files': sorted(set(known_files) - set(inventoried)) if reason is None else None,
            'inventory_measurement_unavailable_reason': reason,
            'unsupported_test_files': unsupported,
            'unavailable_runner_test_files': runner_missing}


def evaluate_worker(manifest, checkout, radar, timeout, repeats, oracle_only=False):
    """Called only in the disposable container by the fixed worker entrypoint."""
    # The read-only checkout belongs to the host UID, while the container uses
    # an unprivileged UID. Trust this one explicit input, never arbitrary paths.
    ev.GIT_ENV.update(GIT_CONFIG_COUNT='1', GIT_CONFIG_KEY_0='safe.directory',
                      GIT_CONFIG_VALUE_0=str(checkout),
                      GIT_AUTHOR_DATE='2000-01-01T00:00:00Z',
                      GIT_COMMITTER_DATE='2000-01-01T00:00:00Z')
    Path('/tmp/home').mkdir(exist_ok=True)
    result = {'schema_version': 1, 'repository': manifest['repository'], 'rows': [],
              'execution_kind': 'oracle_only' if oracle_only else 'radar_evaluation',
              'cpu_affinity': worker_affinity(),
              'runtime': manifest.get('runtime', {}), 'baseline': [],
              'tool_versions': {name: ev.run(argv, Path('/tmp'), 10) for name, argv in (
                  ('python', ['python3', '--version']), ('git', ['git', '--version']),
                  ('go', ['go', 'version']), ('node', ['node', '--version']),
                  ('npm', ['npm', '--version']), ('pytest', ['python3', '-m', 'pytest', '--version']),
                  ('radar', [str(radar), '--version']))},
              'measurement_limits': [
                  'Cold means a fresh disposable checkout/container; host page cache is not flushed.',
                  'Warm repeats reuse in-container tool caches; Docker startup excluded from worker timing.',
                  'Peak memory is GNU time process maximum RSS, not aggregate container memory.',
                  'Missing per-test-file oracle means recall and precision are unavailable.',
                  'Native full-suite commands determine ground truth; supplementary per-file failures only invalidate file metrics.',
                  'Each independent suite rejects tracked source drift and HEAD changes; untracked additions and transient restored edits are not detected.',
                  'Independent mutations are controlled challenges, not a representative defect distribution.']}
    with tempfile.TemporaryDirectory(prefix='external-eval-') as tmp:
        root = Path(tmp)
        archive, repo = root / 'source.tar', root / 'repo'
        ev.git(checkout, 'cat-file', '-e', manifest['repository']['revision'] + '^{commit}')
        result['upstream_tree'] = ev.git(checkout, 'rev-parse', manifest['repository']['revision'] + '^{tree}')
        ev.git(checkout, 'archive', '--format=tar', '--output=' + str(archive), manifest['repository']['revision'])
        repo.mkdir()
        with tarfile.open(archive) as source:
            extract_snapshot(source, repo)
        ev.git(repo, 'init', '-q')
        ev.git(repo, 'add', '.')
        ev.git(repo, 'commit', '-qm', 'Pinned upstream snapshot')
        base = ev.git(repo, 'rev-parse', 'HEAD')
        result['base_tree'] = ev.git(repo, 'rev-parse', 'HEAD^{tree}')
        result['snapshot_matches_upstream_tree'] = result['base_tree'] == result['upstream_tree']
        result['base_provenance'] = 'synthetic commit of exact pinned Git archive'
        # Images can supply immutable dependencies outside the source tree.
        # Source repositories and host cache directories are never writable mounts.
        dependency_links = manifest.get('runtime', {}).get('dependency_links', [])
        for link in dependency_links:
            destination = ev.confined(repo, link['path'])
            target = Path(link['target'])
            if not target.is_absolute() or not str(target).startswith('/opt/'):
                raise ValueError('dependency link target must be under /opt/')
            if destination.exists() or destination.is_symlink():
                raise ValueError('dependency link would replace upstream file')
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.symlink_to(target)
        # Only the native full suite determines candidate truth. A supplementary
        # per-file oracle can be non-portable because of upstream process/order
        # coupling; that invalidates file metrics, not observed full-suite truth.
        commands = manifest['full_suite']
        file_commands = manifest.get('file_oracle') or (commands if all(len(command.get('test_files', [])) == 1 for command in commands) else [])
        result['full_suite_definition'] = commands
        result['file_oracle_definition'] = file_commands
        for repeat in range(repeats):
            result['baseline'].append({'cache': 'cold_checkout' if repeat == 0 else 'warm',
                                       'tests': suite(repo, commands, timeout)})
        absent_oracle = {'status': 'not_declared', 'commands': [], 'seconds': None, 'peak_process_rss_kib': None}
        result['baseline_oracle'] = (suite(repo, file_commands, timeout) if file_commands != commands else result['baseline'][0]['tests']) if file_commands else absent_oracle
        for index, case in enumerate(manifest['cases']):
            row = {'id': case['id'], 'category': case.get('category'),
                   'hypothesis': case.get('ground_truth'), 'defect_expected': case.get('defect_expected'),
                   'branches': [], 'modes': [], 'ground_truth_status': 'unavailable',
                   'integration_only': False}
            result['rows'].append(row)
            try:
                branches = []
                for agent, edits in enumerate(case['agents']):
                    branch = f'case-{index}-agent-{agent}'
                    ev.git(repo, 'checkout', '-qb', branch, base)
                    for edit in edits:
                        path = ev.confined(repo, edit['path'])
                        if path.is_symlink():
                            raise ValueError('mutation refuses symlink')
                        old = path.read_text()
                        count = old.count(edit['find'])
                        if count == 0 or (count != 1 and not edit.get('all')):
                            raise ValueError('mutation requires unique expected anchor: ' + edit['path'])
                        path.write_text(old.replace(edit['find'], edit['replace'], -1 if edit.get('all') else 1))
                    ev.git(repo, 'add', '--', *[edit['path'] for edit in edits])
                    ev.git(repo, 'commit', '-qm', case['id'])
                    branches.append(branch)
                    row['branches'].append({'commit': ev.git(repo, 'rev-parse', 'HEAD'),
                                            'tree': ev.git(repo, 'rev-parse', 'HEAD^{tree}'),
                                            'tests': suite(repo, commands, timeout)})
                ev.git(repo, 'checkout', '-qb', f'combined-{index}', base)
                merge_started = time.monotonic()
                for branch in branches:
                    ev.git(repo, 'merge', '--no-edit', '--no-ff', branch)
                row['merge_seconds'] = time.monotonic() - merge_started
                row['combined_tree'] = ev.git(repo, 'rev-parse', 'HEAD^{tree}')
                row['combined_tests'] = suite(repo, commands, timeout)
                row['ground_truth_status'] = row['combined_tests']['status']
                # Per-file oracle only exists when explicitly declared commands map
                # their whole outcome to independently executed test files.
                oracle = (suite(repo, file_commands, timeout) if file_commands != commands else row['combined_tests']) if file_commands else absent_oracle
                row['oracle'] = oracle
                baseline_passed = all(b['tests']['status'] == 'passed' for b in result['baseline'])
                row['baseline_oracle_consistent'] = all(b['tests']['status'] == result['baseline_oracle']['status'] for b in result['baseline']) if file_commands else None
                row['combined_oracle_consistent'] = oracle['status'] == row['combined_tests']['status'] if file_commands else None
                row['ground_truth_usable'] = baseline_passed and row['ground_truth_status'] in ('passed', 'failed')
                if not baseline_passed:
                    row['unavailable_reason'] = 'Authoritative native full-suite baseline did not pass.'
                row['integration_only'] = bool(row['ground_truth_usable'] and len(branches) >= 2
                    and row['ground_truth_status'] == 'failed'
                    and all(b['tests']['status'] == 'passed' for b in row['branches']))
                row['failing_test_files'] = sorted({p for c in oracle['commands']
                    if c['recognized_result'] == 'failed' and len(c.get('test_files', [])) == 1
                    for p in c.get('test_files', [])})
                row['test_file_oracle_available'] = bool(file_commands and all(len(c.get('test_files', [])) == 1 for c in oracle['commands'])
                    and oracle['status'] in ('passed', 'failed') and result['baseline_oracle']['status'] == 'passed'
                    and row['combined_oracle_consistent'])
                row['file_oracle_unavailable_reason'] = None if row['test_file_oracle_available'] else (
                    'No independent per-file oracle declared.' if not file_commands else
                    'Per-file baseline does not pass; native-suite results remain authoritative.' if result['baseline_oracle']['status'] != 'passed' else
                    'Per-file and native candidate results disagree.' if row['combined_oracle_consistent'] is False else
                    'Per-file oracle has missing file bindings or unavailable outcomes.')
                known_files = sorted({path for command in file_commands for path in command.get('test_files', [])}) if row['test_file_oracle_available'] else None
                row['file_oracle_source'] = 'independent_per_file_commands' if row['test_file_oracle_available'] else None
                baseline_labels = [command.get('junit') for command in result['baseline'][0]['tests']['commands']]
                candidate_labels = [command.get('junit') for command in row['combined_tests']['commands']]
                if baseline_passed and row['ground_truth_usable'] and baseline_labels and candidate_labels and all(label and label.get('available') for label in baseline_labels + candidate_labels):
                    row['supplementary_file_oracle_available'] = row['test_file_oracle_available']
                    row['supplementary_file_oracle_unavailable_reason'] = row['file_oracle_unavailable_reason']
                    row['test_file_oracle_available'] = True
                    row['file_oracle_source'] = 'native_full_suite_junit'
                    row['file_oracle_unavailable_reason'] = None
                    row['failing_test_files'] = sorted({path for label in candidate_labels for path in label['failing_test_files']})
                    known_files = sorted({path for label in baseline_labels + candidate_labels for path in label['test_files']})
                conventional = manifest.get('baseline_suite') or commands
                row['conventional_baseline_tests'] = suite(repo, conventional, timeout) if conventional != commands else row['combined_tests']
                row['conventional_baseline_definition'] = conventional
                if oracle_only:
                    row['radar_status'] = 'not_requested'
                    continue
                combined = ev.git(repo, 'rev-parse', 'HEAD')
                for mode in MODES:
                    analysis = ev.run([str(radar), '--root', str(repo), '--json', 'check', '--base', base,
                                       '--head', combined, '--suite', mode], repo, timeout)
                    selection_report = parsed(analysis)
                    for repeat in range(repeats):
                        ev.git(repo, 'checkout', '-q', '--detach', base)
                        runtime = ev.run([str(radar), '--root', str(repo), '--json', 'gate', '--base', base,
                                          '--run', '--suite', mode, '--timeout', str(timeout) + 's', *branches], repo, timeout)
                        report = parsed(runtime)
                        selection = (report or selection_report or {}).get('selection') or {}
                        candidate_matches = (report or {}).get('candidate_tree') == row['combined_tree'] if report else None
                        row['modes'].append({'mode': mode, 'repeat': repeat,
                            'cache': 'warm_tool_cache' if repeat else 'first_mode_run',
                            'analysis': analysis, 'selection_report': selection_report,
                            'runtime': runtime, 'report': report,
                            'runner_status': runtime['status'],
                            'runner_exit_code': runtime.get('exit_code'),
                            **inventory_metrics(report, selection_report, known_files, row['file_oracle_source']),
                            'verdict': (report or {}).get('gate', {}).get('verdict'),
                            'candidate_tree_matches_ground_truth': candidate_matches,
                            'selected_test_files': selected_files(report or selection_report),
                            'selection_available': bool(selection),
                            'inventory_test_files': selection.get('inventory_test_files'),
                            'test_file_selection_fraction': ev.metric(len(selected_files(report or selection_report)),
                                selection.get('inventory_test_files') or 0),
                            'uncovered_changes': selection.get('uncovered_changes'),
                            'end_to_end_seconds': runtime['seconds'],
                            'analysis_overhead_seconds': analysis['seconds'],
                            **execution_timing(report, runtime['seconds']),
                            'peak_process_rss_kib': runtime.get('peak_rss_kib'),
                            'conventional_baseline_seconds': row['merge_seconds'] + row['conventional_baseline_tests']['seconds']})
            except (ValueError, OSError, KeyError) as error:
                row['unavailable_reason'] = str(error)
                # Restore only our disposable repository after a merge conflict.
                ev.git(repo, 'reset', '--hard', base)
    result['summary'] = summarize(result['rows'], oracle_only)
    return result


def summarize(rows, oracle_only=False):
    def verdict(measurement):
        # A verdict on a different tree cannot substantiate this oracle result.
        return None if measurement.get('candidate_tree_matches_ground_truth') is False else measurement.get('verdict')

    result = {}
    for mode in MODES:
        observations = [(r, next((m for m in r['modes'] if m['mode'] == mode and m['repeat'] == 0), {})) for r in rows]
        observed = [(r, m) for r, m in observations if r.get('ground_truth_usable')]
        bad = [(r, m) for r, m in observed if r['ground_truth_status'] == 'failed']
        good = [(r, m) for r, m in observed if r['ground_truth_status'] == 'passed']
        integration = [(r, m) for r, m in bad if r['integration_only']]
        oracle = [(r, m) for r, m in observed if r.get('test_file_oracle_available') and m.get('selection_available')]
        failing = sum(len(r['failing_test_files']) for r, _ in oracle)
        selected = sum(len(m.get('selected_test_files', [])) for _, m in oracle)
        hits = sum(len(set(r['failing_test_files']) & set(m.get('selected_test_files', []))) for r, m in oracle)
        result[mode] = {'observed_ground_truth': len(observed),
            'unavailable_ground_truth': len(rows) - len(observed),
            'integration_defect_detection': ev.metric(sum(verdict(m) == 'fail' for _, m in integration), len(integration)),
            'mutation_defect_detection': ev.metric(sum(verdict(m) == 'fail' for _, m in bad), len(bad)),
            'false_pass_rate': ev.metric(sum(verdict(m) == 'pass' for _, m in bad), len(bad)),
            'false_fail_rate': ev.metric(sum(verdict(m) == 'fail' for _, m in good), len(good)),
            'false_block_rate': ev.metric(sum(verdict(m) == 'blocked' for _, m in good), len(good)),
            'test_selection_recall': ev.metric(hits, failing),
            'test_selection_precision': ev.metric(hits, selected),
            'candidate_tree_mismatches': sum(m.get('candidate_tree_matches_ground_truth') is False for _, m in observations),
            'radar_unavailable': sum(verdict(m) is None for _, m in observations)}
        if oracle_only:
            for key in ('integration_defect_detection', 'mutation_defect_detection', 'false_pass_rate',
                        'false_fail_rate', 'false_block_rate', 'test_selection_recall', 'test_selection_precision'):
                result[mode][key] = ev.metric(0, 0)
            result[mode]['known_integration_only_defects'] = len(integration)
            result[mode]['radar_status'] = 'not_requested'
            result[mode]['radar_unavailable'] = None
    return result


def docker_command(image, checkout, radar, manifest_path, output, timeout, repeats, name, harness_root=BASE, oracle_only=False, writable_cache_dirs=()):
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', image):
        raise ValueError('Docker image must be pinned local sha256 image ID')
    command = ['docker', 'run', '--name', name, '--rm', '--pull=never', '--network=none',
        '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--pids-limit=256',
        '--memory=4g', '--cpus=2', '--user=65534:65534',
        '--tmpfs=/tmp:rw,exec,nosuid,size=2g,mode=1777', '--env=HOME=/tmp/home',
        '--env=RADAR_CONFIG_DIR=/tmp/radar-config', '--env=RADAR_STATE_DIR=/tmp/radar-state',
        '--env=GIT_CONFIG_GLOBAL=/dev/null', '--env=GIT_CONFIG_NOSYSTEM=1',
        '--env=GOTOOLCHAIN=local', '--env=GOCACHE=/tmp/go-cache',
        '--mount', f'type=bind,src={checkout},dst=/source,readonly',
        '--mount', f'type=bind,src={radar},dst=/opt/radar,readonly',
        '--mount', f'type=bind,src={harness_root},dst=/harness,readonly',
        '--mount', f'type=bind,src={manifest_path},dst=/input/manifest.json,readonly',
        '--mount', f'type=bind,src={output},dst=/output',
        *cache_tmpfs_options(writable_cache_dirs),
        '--entrypoint=python3', image, '/harness/external/run.py', '--worker',
        '--manifest', '/input/manifest.json', '--checkout', '/source', '--radar', '/opt/radar',
        '--output', '/output/result.json', '--timeout', str(timeout), '--repeats', str(repeats)]
    if oracle_only:
        command.append('--oracle-only')
    return command


def markdown(result):
    lines = ['# External Radar evaluation', '',
             'Project: ' + result['repository']['name'],
             'Revision: `' + result['repository']['revision'] + '`', '',
             'Execution: ' + result.get('execution_status', 'completed'), '',
             'Kind: ' + result.get('execution_kind', 'radar_evaluation'), '',
             '| Case | Ground truth | Integration only | Targeted | Balanced | Full | Runner status | Unavailable reason | File oracle |',
             '| --- | --- | --- | --- | --- | --- | --- | --- | --- |']
    for row in result.get('rows', []):
        fallback = 'not requested' if result.get('execution_kind') == 'oracle_only' else 'unavailable'
        verdicts = [next((m.get('verdict') or 'unavailable' for m in row['modes'] if m['mode'] == mode), fallback) for mode in MODES]
        ground_truth = row['ground_truth_status'] if row.get('ground_truth_usable') else 'unavailable (' + row['ground_truth_status'] + ')'
        statuses = ', '.join(sorted({measurement.get('runner_status', 'unavailable') for measurement in row['modes']})) or 'unavailable'
        reason = (row.get('unavailable_reason') or '').replace('|', '/').replace('\n', ' ')
        file_oracle = 'available' if row.get('test_file_oracle_available') else (row.get('file_oracle_unavailable_reason') or 'unavailable')
        lines.append('| ' + ' | '.join([row['id'], ground_truth, str(row['integration_only']), *verdicts, statuses, reason, file_oracle]) + ' |')
    if result.get('unavailable_reason'):
        lines += ['', 'Unavailable: ' + result['unavailable_reason']]
    lines += ['', '| Mode | Observed ground truth | Integration detection | Mutation detection | False PASS | False FAIL | False BLOCK | Recall | Precision |',
              '| --- | --- | --- | --- | --- | --- | --- | --- | --- |']
    for mode, summary in result.get('summary', {}).items():
        values = []
        for key in ('integration_defect_detection', 'mutation_defect_detection', 'false_pass_rate',
                    'false_fail_rate', 'false_block_rate', 'test_selection_recall', 'test_selection_precision'):
            value = summary[key]
            values.append(f"{value['numerator']}/{value['denominator']}" if value['denominator'] else 'unavailable')
        lines.append('| ' + ' | '.join([mode, str(summary['observed_ground_truth']), *values]) + ' |')
    lines += ['', '| Case | Mode | Cache | Radar elapsed s | Conventional full-suite s | Analysis s | Selected execution s | Peak process RSS KiB |',
              '| --- | --- | --- | --- | --- | --- | --- | --- |']
    for row in result.get('rows', []):
        for measurement in row.get('modes', []):
            values = [row['id'], measurement['mode'], measurement['cache']]
            for key in ('end_to_end_seconds', 'conventional_baseline_seconds', 'analysis_overhead_seconds',
                        'selected_test_execution_seconds', 'peak_process_rss_kib'):
                value = measurement.get(key)
                values.append('unavailable' if value is None else str(round(value, 4)))
            lines.append('| ' + ' | '.join(values) + ' |')
    lines += ['', 'Small controlled samples do not establish general reliability. Null rates have no usable denominator.',
              'Test-file selection reduction does not imply runtime savings. Timing includes independent measurements in JSON.', '']
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--checkout', required=True, type=Path)
    parser.add_argument('--radar', required=True, type=Path)
    parser.add_argument('--image')
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--timeout', type=int, default=600)
    parser.add_argument('--repeats', type=int, default=2)
    parser.add_argument('--oracle-only', action='store_true', help='Execute independent native truth suites only; request no Radar analysis or gates')
    parser.add_argument('--worker', action='store_true', help=argparse.SUPPRESS)
    args = parser.parse_args()
    manifest = load_manifest(args.manifest)
    if args.timeout <= 0 or args.repeats <= 0:
        parser.error('timeout and repeats must be positive')
    if args.worker:
        if not Path('/.dockerenv').exists() or not Path('/harness/external/run.py').exists():
            parser.error('worker entrypoint requires disposable Docker environment')
        result = evaluate_worker(manifest, args.checkout, args.radar, args.timeout, args.repeats, args.oracle_only)
    else:
        started = time.monotonic()
        result = {'schema_version': 1, 'repository': manifest['repository'], 'rows': [],
                  'execution_kind': 'oracle_only' if args.oracle_only else 'radar_evaluation',
                  'execution_status': 'unavailable', 'summary': summarize([])}
        image = args.image or manifest.get('runtime', {}).get('image', '')
        name = 'radar-external-' + os.urandom(8).hex()
        try:
            with tempfile.TemporaryDirectory(prefix='radar-external-output-') as tmp:
                temporary = Path(tmp)
                output = temporary / 'output'
                output.mkdir()
                output.chmod(0o777)
                # Read-only binds do not freeze host files. Copy every mutable
                # harness input so recorded hashes describe exactly what ran.
                harness = temporary / 'harness'
                (harness / 'external').mkdir(parents=True)
                shutil.copy2(__file__, harness / 'external/run.py')
                shutil.copy2(BASE / 'evaluate.py', harness / 'evaluate.py')
                frozen_manifest, frozen_radar = temporary / 'manifest.json', temporary / 'radar'
                shutil.copy2(args.manifest, frozen_manifest)
                shutil.copy2(args.radar, frozen_radar)
                captured_hashes = {'manifest_sha256': digest(frozen_manifest),
                    'harness_sha256': digest(harness / 'external/run.py'),
                    'utility_sha256': digest(harness / 'evaluate.py'),
                    'radar_sha256': digest(frozen_radar)}
                manifest = load_manifest(frozen_manifest)
                source_provenance = {'head': ev.git(ev.ROOT, 'rev-parse', 'HEAD'),
                    'status': ev.git(ev.ROOT, 'status', '--porcelain=v1'),
                    'unstaged_diff_sha256': hashlib.sha256(ev.git(ev.ROOT, 'diff', '--binary').encode()).hexdigest(),
                    'staged_diff_sha256': hashlib.sha256(ev.git(ev.ROOT, 'diff', '--cached', '--binary').encode()).hexdigest(),
                    'binary_build_metadata': ev.run(['go', 'version', '-m', str(frozen_radar)], ev.ROOT, 10)}
                argv = docker_command(image, args.checkout.resolve(), frozen_radar, frozen_manifest, output, args.timeout, args.repeats, name, harness, args.oracle_only,
                                      manifest.get('runtime', {}).get('writable_cache_dirs', []))
                # Bound total runtime as well as every worker subprocess.
                command_count = len(manifest['cases']) * (len(manifest['full_suite']) * 5 + len(manifest.get('file_oracle', [])) + len(manifest.get('baseline_suite', [])) + 3 * args.repeats + 3) + args.repeats * len(manifest['full_suite']) + len(manifest.get('file_oracle', []))
                execution = ev.run(argv, BASE, args.timeout * command_count + 60)
                if (output / 'result.json').exists():
                    result = json.loads((output / 'result.json').read_text())
                    result['execution_status'] = 'completed' if execution['exit_code'] == 0 else execution['status']
                else:
                    result['unavailable_reason'] = execution['stderr'] or execution['status']
                result['docker_execution'] = execution
        except (OSError, ValueError) as error:
            result['unavailable_reason'] = str(error)
        finally:
            # Killing the Docker client alone does not reliably kill its container.
            try:
                subprocess.run(['docker', 'rm', '-f', name], capture_output=True, timeout=15)
            except (OSError, subprocess.TimeoutExpired):
                pass
        result['provenance'] = {'image': image, **locals().get('captured_hashes', {}),
            'source_checkout_snapshot': locals().get('source_provenance'),
            'network': 'none', 'elapsed_seconds': time.monotonic() - started}
        if result['execution_status'] != 'completed' and not result['rows']:
            result['rows'] = [{'id': case['id'], 'modes': [], 'ground_truth_status': 'unavailable',
                              'integration_only': False, 'unavailable_reason': result.get('unavailable_reason')}
                             for case in manifest['cases']]
            result['summary'] = summarize(result['rows'], args.oracle_only)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    args.output.with_suffix('.md').write_text(markdown(result))
    return 0 if result.get('execution_status', 'completed') == 'completed' else 2


if __name__ == '__main__':
    sys.exit(main())
