#!/usr/bin/env python3
"""Offline pinned-candidate evaluation; external execution requires an isolation wrapper.

No acquisition, dependency installation, shell commands, or implicit network calls.
Manifest paths are relative to the manifest, edits confined to private repositories.
"""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import shutil
import signal
import subprocess
import tempfile
import time

ROOT = pathlib.Path(__file__).resolve().parent.parent
GIT_ENV = dict(os.environ, PYTHONDONTWRITEBYTECODE='1', GOTOOLCHAIN='local', GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
               GIT_NO_REPLACE_OBJECTS='1', GIT_NO_LAZY_FETCH='1', GIT_TERMINAL_PROMPT='0',
               GIT_AUTHOR_NAME='Radar evaluation', GIT_AUTHOR_EMAIL='eval@example.invalid',
               GIT_COMMITTER_NAME='Radar evaluation', GIT_COMMITTER_EMAIL='eval@example.invalid')
for key in list(GIT_ENV):
    if key.startswith('GIT_') and key not in {'GIT_CONFIG_NOSYSTEM', 'GIT_CONFIG_GLOBAL',
       'GIT_NO_REPLACE_OBJECTS', 'GIT_NO_LAZY_FETCH', 'GIT_TERMINAL_PROMPT',
       'GIT_AUTHOR_NAME', 'GIT_AUTHOR_EMAIL', 'GIT_COMMITTER_NAME', 'GIT_COMMITTER_EMAIL'}:
        del GIT_ENV[key]


def run(argv, cwd, timeout=120, wrapper=None, max_output_bytes=16 * 1024 * 1024):
    """File-backed bounded output; GNU time max RSS is not aggregate tree RSS."""
    started = time.monotonic()
    record = {'argv': argv, 'cwd': str(cwd), 'seconds': None, 'peak_rss_kib': None,
              'max_output_bytes': max_output_bytes}
    with tempfile.TemporaryDirectory(prefix='radar-measure-') as tmp:
        rss = pathlib.Path(tmp) / 'rss'
        stdout_path, stderr_path = pathlib.Path(tmp) / 'stdout', pathlib.Path(tmp) / 'stderr'
        command = list(wrapper or []) + argv
        if platform.system() == 'Linux' and pathlib.Path('/usr/bin/time').exists():
            command = ['/usr/bin/time', '-f', '%M', '-o', str(rss), '--'] + command
        try:
            with stdout_path.open('wb') as stdout_file, stderr_path.open('wb') as stderr_file:
                proc = subprocess.Popen(command, cwd=cwd, env=GIT_ENV, stdout=stdout_file,
                                        stderr=stderr_file, start_new_session=os.name == 'posix')
                status = 'completed'
                while True:
                    emitted = stdout_path.stat().st_size + stderr_path.stat().st_size
                    if emitted > max_output_bytes:
                        status = 'output_limit'
                    elif time.monotonic() - started > timeout:
                        status = 'timeout'
                    if status != 'completed':
                        if proc.poll() is None:
                            try:
                                if os.name == 'posix':
                                    os.killpg(proc.pid, signal.SIGKILL)
                                else:
                                    proc.kill()
                            except ProcessLookupError:
                                pass
                        proc.wait()
                        break
                    if proc.poll() is not None:
                        if os.name == 'posix':
                            try:
                                os.killpg(proc.pid, signal.SIGKILL)
                            except ProcessLookupError:
                                pass
                        break
                    time.sleep(0.02)
            if stdout_path.stat().st_size + stderr_path.stat().st_size > max_output_bytes:
                status = 'output_limit'
            with stdout_path.open('rb') as f:
                stdout_bytes = f.read(max_output_bytes)
            with stderr_path.open('rb') as f:
                stderr_bytes = f.read(max(0, max_output_bytes - len(stdout_bytes)))
            record.update(status=status, exit_code=proc.returncode,
                          stdout=stdout_bytes.decode('utf-8', errors='replace'),
                          stderr=stderr_bytes.decode('utf-8', errors='replace'),
                          emitted_output_bytes=stdout_path.stat().st_size + stderr_path.stat().st_size,
                          output_truncated=status == 'output_limit')
            if rss.exists():
                values = rss.read_text().splitlines()
                if values and values[-1].isdigit():
                    record['peak_rss_kib'] = int(values[-1])
        except OSError as exc:
            record.update(status='environment_unavailable', exit_code=None, stdout='', stderr=str(exc))
    if record.get('exit_code') == 127:
        record['status'] = 'environment_unavailable'
    record['seconds'] = round(time.monotonic() - started, 6)
    return record


def git(repo, *args):
    argv = ['git', '-C', str(repo), '-c', 'core.hooksPath=' + os.devnull,
            '-c', 'core.fsmonitor=false', '-c', 'core.attributesFile=' + os.devnull,
            '-c', 'commit.gpgSign=false', '-c', 'protocol.allow=never', *args]
    p = subprocess.run(argv, capture_output=True, text=True, env=GIT_ENV)
    if p.returncode:
        raise ValueError('Git ' + ' '.join(args) + ': ' + (p.stderr.strip() or p.stdout.strip() or 'exit ' + str(p.returncode)))
    return p.stdout.strip()


def confined(root, relative):
    path = (root / relative).resolve()
    if not path.is_relative_to(root.resolve()):
        raise ValueError('path escapes repository: ' + relative)
    return path


def recognize(command):
    output = command.get('stdout', '') + command.get('stderr', '')
    kind = command.get('recognizer')
    if command['status'] != 'completed':
        return 'environment_unavailable'
    if kind == 'unittest':
        matched = re.search(r'Ran (\d+) tests?', output)
    elif kind == 'node':
        matched = re.search(r'(?:#|ℹ) tests (\d+)', output)
    elif kind == 'go':
        if '[setup failed]' in output or '[build failed]' in output:
            return 'environment_unavailable'
        events = []
        for line in output.splitlines():
            try:
                event = json.loads(line)
                if isinstance(event, dict) and event.get('Test') and event.get('Action') in ('pass', 'fail'):
                    events.append(event)
            except ValueError:
                pass
        matched = events or re.search(r'--- (?:PASS|FAIL):', output)
    elif kind == 'pytest':
        if command['exit_code'] not in (0, 1):
            return 'environment_unavailable'
        matched = re.search(r'\d+ (?:passed|failed|error)', output)
    elif kind in ('jest', 'vitest'):
        matched = re.search(r'Tests:?[ \t]+(\d+) (?:passed|failed)', output)
        if matched and int(matched[1]) == 0:
            return 'ground_truth_unavailable'
    elif kind == 'cargo':
        matched = re.search(r'test result: (?:ok|FAILED)\. (\d+) passed; (\d+) failed', output)
        if matched and int(matched[1]) + int(matched[2]) == 0:
            return 'ground_truth_unavailable'
    else:
        return 'analysis_unsupported'
    if not matched or (kind in ('unittest', 'node') and int(matched[1]) == 0):
        return 'ground_truth_unavailable'
    return 'passed' if command['exit_code'] == 0 else 'failed'


def suite(repo, commands, timeout, wrapper):
    observations = []
    for spec in commands:
        cwd = confined(repo, spec.get('cwd', '.'))
        record = run(spec['argv'], cwd, timeout, wrapper)
        record.update(test_files=spec.get('test_files', []), recognizer=spec.get('recognizer'))
        record['recognized_result'] = recognize(record)
        observations.append(record)
    return observations


def suite_state(observations):
    if not observations:
        return 'no_relevant_test_executed'
    for status in ('environment_unavailable', 'analysis_unsupported', 'ground_truth_unavailable'):
        if any(r['recognized_result'] == status for r in observations):
            return status
    return 'failed' if any(r['recognized_result'] == 'failed' for r in observations) else 'passed'


def metric(numerator, denominator):
    return {'numerator': numerator, 'denominator': denominator,
            'value': numerator / denominator if denominator else None}


def summarize(rows):
    observed = [r for r in rows if r.get('ground_truth_status') in ('passed', 'failed')]
    defective = [r for r in observed if r['ground_truth_status'] == 'failed']
    clean = [r for r in observed if r['ground_truth_status'] == 'passed']
    integration_defective = [r for r in defective if len(r.get('individual_branches', [])) >= 2
      and all(suite_state(b['tests']) == 'passed' for b in r['individual_branches'])]
    failed_files = sum(len(r['failing_test_files']) for r in defective)
    hits = sum(len(set(r['failing_test_files']) & set(r.get('selected_test_files', []))) for r in defective)
    executed_hits = sum(len(set(r['failing_test_files']) & set(r.get('recognized_failed_test_files', []))) for r in defective)
    coverage_rows = [r for r in rows if r.get('coverage_measured')]
    selected = sum(len(r.get('selected_test_files', [])) for r in observed)
    inventory = sum(len(r.get('complete_suite_test_files', [])) for r in observed)
    selection_measured = [r for r in observed if r.get('complete_suite_test_files')]
    relative_selected = sum(len(r.get('selected_test_files', [])) for r in selection_measured)
    return {
      'samples': len(rows), 'ground_truth_observed': len(observed),
      'unavailable_samples': [{'id': r['id'], 'classification': r['classification']} for r in rows if r not in observed],
      'integration_defect_detection_rate': metric(sum(r.get('verdict') == 'fail' for r in integration_defective), len(integration_defective)),
      'integration_eligible_cases': [r['id'] for r in integration_defective],
      'mutation_defect_detection_rate': metric(sum(r.get('verdict') == 'fail' for r in defective), len(defective)),
      'known_failing_candidate_count': len(defective),
      'known_failing_test_recall': metric(executed_hits, failed_files),
      'false_positive_rate': metric(sum(r.get('verdict') == 'fail' for r in clean), len(clean)),
      'false_block_rate': metric(sum(r.get('verdict') == 'blocked' for r in clean), len(clean)),
      'uncovered_change_rate': metric(sum(r.get('uncovered_change_count', 0) for r in coverage_rows), sum(r.get('changed_count', 0) for r in coverage_rows)),
      'test_selection_precision': metric(hits, selected),
      'test_selection_recall': metric(hits, failed_files),
      'selected_relative_to_complete_suite': metric(relative_selected, inventory),
      'radar_analysis_overhead_seconds': [r['analysis']['seconds'] + r.get('selection_analysis', {}).get('seconds', 0) for r in rows if 'analysis' in r],
      'total_verification_wall_seconds': [r['verification_wall_seconds'] for r in rows if 'verification_wall_seconds' in r],
      'baseline_full_suite_wall_seconds': [r['baseline_wall_seconds'] for r in rows if r.get('baseline_wall_seconds') is not None],
      'peak_memory_kib': [r['analysis'].get('peak_rss_kib') for r in rows if 'analysis' in r],
      'runtime_gate_peak_memory_kib': [r['runtime_gate'].get('peak_rss_kib') for r in rows if 'runtime_gate' in r]
    }


def evaluate(manifest, manifest_path, radar, allow_execution=False, wrapper=None, attestation=None, timeout=120):
    if manifest.get('schema_version') != 1:
        raise ValueError('unsupported manifest schema_version')
    source = manifest['repository']
    rows = []
    environment = {'platform': platform.platform(), 'python': platform.python_version(),
                   'radar': str(radar), 'radar_sha256': hashlib.sha256(radar.read_bytes()).hexdigest() if radar.is_file() else None,
                   'harness_source_revision': git(ROOT, 'rev-parse', 'HEAD'),
                   'harness_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                   'manifest_sha256': hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest(),
                   'go_cache': GIT_ENV.get('GOCACHE'), 'execution_requested': allow_execution,
                   'isolation_wrapper': wrapper, 'isolation_attestation': attestation,
                   'tool_versions': {name: run(argv, ROOT, timeout=5) for name, argv in
                     [('git', ['git', '--version']), ('go', ['go', 'version']), ('node', ['node', '--version']),
                      ('cargo', ['cargo', '--version']), ('python', ['python3', '--version'])]},
                   'network': 'No acquisition or installation by harness; execution wrapper must enforce network isolation.'}
    result = {'schema_version': 1, 'repository': source, 'environment': environment,
              'full_suite_definition': manifest.get('full_suite', []),
              'baseline_suite_definition': manifest.get('baseline_suite', []), 'rows': rows}
    fixture = source.get('fixture')
    source_path = (manifest_path.parent / (fixture or source.get('local_checkout', ''))).resolve()
    trusted = bool(fixture and (source_path.is_relative_to(ROOT / 'benchmarks/fixtures') or source_path == ROOT / 'examples/selection-eval/fixture'))
    authorized = allow_execution and (trusted or bool(wrapper and attestation))
    try:
        if not source_path.is_dir():
            raise ValueError('checkout unavailable: ' + str(source_path))
        if not fixture:
            revision = source.get('revision', '')
            if not re.fullmatch(r'[0-9a-f]{40}', revision):
                raise ValueError('external revision must be a full immutable 40-character SHA')
            git(source_path, 'cat-file', '-e', revision + '^{commit}')
        with tempfile.TemporaryDirectory(prefix='radar-benchmark-') as tmp:
            repo = pathlib.Path(tmp) / 'repo'
            if fixture:
                shutil.copytree(source_path, repo, ignore=shutil.ignore_patterns('__pycache__', 'state.db', 'state.db-shm', 'state.db-wal', '.git'))
                git(repo, 'init', '-q')
                git(repo, 'add', '.')
                git(repo, 'commit', '-qm', 'fixture baseline')
            else:
                # Git archive reads committed blobs; supplied worktree dirt is excluded.
                import tarfile
                archive = pathlib.Path(tmp) / 'source.tar'
                git(source_path, 'archive', '--format=tar', '--output=' + str(archive), source['revision'])
                repo.mkdir()
                with tarfile.open(archive) as tar:
                    tar.extractall(repo, filter='data')
                git(repo, 'init', '-q')
                git(repo, 'add', '.')
                git(repo, 'commit', '-qm', 'pinned external tree ' + source['revision'])
            base = git(repo, 'rev-parse', 'HEAD')
            result['upstream_tree'] = git(source_path, 'rev-parse', source['revision'] + '^{tree}') if not fixture else None
            result['base_provenance'] = 'synthetic commit of pinned upstream archive' if not fixture else 'synthetic commit of checked-in fixture files'
            result['evaluated_base_commit'] = base
            result['evaluated_base_tree'] = git(repo, 'rev-parse', 'HEAD^{tree}')
            result['baseline_tests'] = suite(repo, manifest.get('full_suite', []), timeout, wrapper) if authorized else []
            for case in manifest['cases']:
                row = {'id': case['id'], 'category': case.get('category'), 'ground_truth_definition': case.get('ground_truth'),
                       'defect_expected': case.get('defect_expected'), 'ground_truth_status': 'ground_truth_unavailable',
                       'complete_suite_test_files': sorted({f for c in manifest.get('full_suite', []) for f in c.get('test_files', [])})}
                rows.append(row)
                try:
                    git(repo, 'checkout', '-q', '--detach', base)
                    branches = []
                    row['individual_branches'] = []
                    for i, edits in enumerate(case.get('agents', [])):
                        branch = f'eval-{len(rows)}-{i}'
                        git(repo, 'checkout', '-qb', branch, base)
                        for edit in edits:
                            if (repo / edit['path']).is_symlink():
                                raise ValueError('mutation refuses symlink: ' + edit['path'])
                            path = confined(repo, edit['path'])
                            old = path.read_text()
                            if edit['find'] not in old:
                                raise ValueError('mutation pattern missing: ' + edit['path'])
                            path.write_text(old.replace(edit['find'], edit['replace'], -1 if edit.get('all') else 1))
                        git(repo, 'add', '--', *[e['path'] for e in edits])
                        git(repo, 'commit', '-qm', case['id'])
                        branches.append(branch)
                        row['individual_branches'].append({'commit': git(repo, 'rev-parse', 'HEAD'),
                          'tests': suite(repo, manifest.get('full_suite', []), timeout, wrapper) if authorized else []})
                    if not branches:
                        row.update(classification='analysis_unsupported', error='case has no controlled agent edits')
                        continue
                    git(repo, 'checkout', '-q', '--detach', base)
                    argv = [str(radar), '--root', str(repo), '--json', 'gate', '--base', base, *branches]
                    analysis = run(argv, repo, timeout)
                    row['analysis'] = analysis
                    try:
                        if analysis['status'] != 'completed':
                            raise ValueError('analysis command unavailable: ' + analysis['status'])
                        report = json.loads(analysis['stdout'])
                    except (ValueError, TypeError):
                        row.update(classification='analysis_unsupported', error=analysis['stderr'])
                        continue
                    row['static_report'] = report
                    gate_unsupported = bool(report.get('error') or report.get('status') == 'error' or report.get('gate', {}).get('verdict') == 'error')
                    if gate_unsupported:
                        row['analysis_error'] = report.get('error', 'gate analysis error')
                    proposal = report.get('verification_proposal') or {}
                    row['proposed_test_files'] = sorted({f for c in proposal.get('commands', []) for f in c.get('test_files', [])})
                    row['discovered_test_files'] = [t['path'] for t in proposal.get('inventory', {}).get('tests', [])]
                    selection = report.get('selection') or {}
                    row['selected_test_files'] = sorted({f for c in selection.get('commands', []) for f in c.get('test_files', [])})
                    row['inventory_test_files'] = len(row['discovered_test_files'])
                    row['changed_count'] = len(report.get('changed', []))
                    row['uncovered_change_count'] = len(selection.get('uncovered_changes', []))
                    row['verdict'] = (report.get('gate') or {}).get('verdict')
                    # Materialize a separate ordinary Git merge for static selection.
                    git(repo, 'checkout', '-qb', f'static-combined-{len(rows)}', base)
                    try:
                        for branch in branches:
                            git(repo, 'merge', '--no-edit', '--no-ff', branch)
                    except ValueError as exc:
                        git(repo, 'merge', '--abort')
                        row.update(classification='merge_conflict', error=str(exc))
                        continue
                    combined = git(repo, 'rev-parse', 'HEAD')
                    row['static_combined_commit'] = combined
                    row['static_combined_tree'] = git(repo, 'rev-parse', 'HEAD^{tree}')
                    selected_analysis = run([str(radar), '--root', str(repo), '--json', 'check', '--base', base,
                                             '--head', combined, '--suite', 'balanced', '--max-commands', '16'], repo, timeout)
                    row['selection_analysis'] = selected_analysis
                    try:
                        if selected_analysis['status'] != 'completed':
                            raise ValueError('selection unavailable: ' + selected_analysis['status'])
                        selection_report = json.loads(selected_analysis['stdout'])
                        row['selection_report'] = selection_report
                        selection = selection_report.get('selection') or {}
                        row['inventory_test_files'] = selection.get('inventory_test_files', row['inventory_test_files'])
                        row['selected_test_files'] = sorted({f for c in selection.get('commands', []) for f in c.get('test_files', [])})
                        row['uncovered_change_count'] = len(selection.get('uncovered_changes', []))
                        row['coverage_measured'] = bool(selection)
                    except ValueError:
                        row['selection_unavailable'] = selected_analysis['stderr']
                    git(repo, 'checkout', '-q', '--detach', base)
                    if gate_unsupported:
                        row.update(classification='analysis_unsupported')
                        continue
                    if not authorized:
                        row.update(classification='ground_truth_unavailable', execution_unavailable_reason=
                          'static mode' if not allow_execution else 'external execution requires an isolation wrapper and attestation')
                        continue
                    if suite_state(result['baseline_tests']) != 'passed':
                        row.update(classification=suite_state(result['baseline_tests']), error='baseline is not recognized passing')
                        continue
                    start = time.monotonic()
                    candidate = f'combined-{len(rows)}'
                    git(repo, 'checkout', '-qb', candidate, base)
                    try:
                        for branch in branches:
                            git(repo, 'merge', '--no-edit', '--no-ff', branch)
                    except ValueError as exc:
                        git(repo, 'merge', '--abort')
                        row.update(classification='merge_conflict', error=str(exc))
                        continue
                    row['baseline_merge_commit'] = git(repo, 'rev-parse', 'HEAD')
                    row['baseline_merge_tree'] = git(repo, 'rev-parse', 'HEAD^{tree}')
                    row['git_merge_wall_seconds'] = round(time.monotonic() - start, 6)
                    baseline_commands = manifest.get('baseline_suite', [])
                    baseline_full = suite(repo, baseline_commands, timeout, wrapper)
                    row['baseline_suite'] = baseline_full
                    row['baseline_wall_seconds'] = round(time.monotonic() - start, 6) if baseline_commands else None
                    row['baseline_suite_basis'] = 'explicit grouped CI commands' if baseline_commands else 'comparison unavailable: no baseline_suite defined'
                    oracle_start = time.monotonic()
                    full = suite(repo, manifest.get('full_suite', []), timeout, wrapper)
                    row['ground_truth_wall_seconds'] = round(time.monotonic() - oracle_start, 6)
                    row['baseline_oracle_consistent'] = suite_state(baseline_full) == suite_state(full) if baseline_commands else None
                    row['full_suite'] = full
                    row['ground_truth_status'] = suite_state(full)
                    row['failing_test_files'] = sorted({f for c in full if c['recognized_result'] == 'failed' for f in c['test_files']})
                    row['known_failure_count'] = len(row['failing_test_files'])
                    git(repo, 'checkout', '-q', '--detach', base)
                    start = time.monotonic()
                    runtime = run(argv + ['--run'], repo, timeout, wrapper)
                    row['runtime_gate'] = runtime
                    row['verification_wall_seconds'] = round(time.monotonic() - start, 6)
                    try:
                        if runtime['status'] != 'completed':
                            raise ValueError('runtime command unavailable: ' + runtime['status'])
                        runtime_report = json.loads(runtime['stdout'])
                        row['runtime_report'] = runtime_report
                        row['verdict'] = runtime_report['gate']['verdict']
                        selection = runtime_report.get('selection') or {}
                        row['selected_test_files'] = sorted({f for c in selection.get('commands', []) for f in c.get('test_files', [])})
                        row['uncovered_change_count'] = len(selection.get('uncovered_changes', []))
                        row['recognized_executions'] = [e.get('observation') for e in runtime_report.get('executions', [])]
                        row['recognized_failed_test_files'] = sorted({f for e in runtime_report.get('executions', [])
                          if e.get('observation', {}).get('tests_failed', 0) > 0
                          for c in selection.get('commands', []) if c['command'] == e.get('command')
                          for f in c.get('test_files', [])})
                        row['coverage_measured'] = bool(selection)
                    except (ValueError, KeyError):
                        row.update(classification='environment_unavailable')
                        continue
                    state = row['ground_truth_status']
                    row['classification'] = state if state not in ('passed', 'failed') else (
                      'no_defect_observed' if state == 'passed' else 'defect_detected' if row['verdict'] == 'fail' else 'defect_not_detected')
                    if row['verdict'] == 'error':
                        row['classification'] = 'environment_unavailable'
                    elif state == 'failed' and row['verdict'] != 'fail' and not any(
                         o and o.get('tests_run', 0) > 0 for o in row['recognized_executions']):
                        row['classification'] = 'no_relevant_test_executed'
                    row['detection_basis'] = ('recognized_test_failure' if row['recognized_failed_test_files']
                       else 'supported_static_finding' if row['verdict'] == 'fail' else None)
                except (ValueError, OSError) as exc:
                    row.update(classification='analysis_unsupported', error=str(exc))
    except (ValueError, OSError) as exc:
        rows.extend({'id': c['id'], 'classification': 'environment_unavailable', 'ground_truth_status': 'ground_truth_unavailable', 'error': str(exc)} for c in manifest['cases'])
    result['summary'] = summarize(rows)
    result['limitations'] = [
      'Observed passing tests do not establish the absence of defects.',
      'Recall uses failing test-file groups; Go package commands group files, not individual test cases.',
      'Selection precision counts selected files observed failing, a lower bound on useful selection.',
      'GNU time reports per-command maximum RSS; it is not simultaneous aggregate process-tree memory.',
      'Isolation wrapper and attestation are supplied by the operator; the harness cannot certify isolation.',
      'Independent branch/full-suite runs and Radar execution are repeated costs; no speed claim follows from selection fraction.'
    ]
    return result


def markdown(result):
    lines = ['# Radar 0.4 evaluation', '', 'Repository: `' + result['repository'].get('name', '') + '`',
             '', 'Pinned external revision: `' + result['repository'].get('revision', 'checked-in fixture') + '`',
             '', '| Case | Ground truth | Classification | Gate | Selected / inventory |',
             '| --- | --- | --- | --- | --- |']
    for r in result['rows']:
        lines.append('| {} | {} | {} | {} | {} / {} |'.format(r['id'], r.get('ground_truth_status'),
          r['classification'], r.get('verdict', 'unavailable'), len(r.get('selected_test_files', [])), r.get('inventory_test_files', 'unavailable')))
    lines += ['', '## Environment and ground truth', '',
              'Platform: `' + result['environment']['platform'] + '`; Python: `' + result['environment']['python'] + '`.',
              '', 'Radar executable SHA256: `' + str(result['environment']['radar_sha256']) + '`.',
              '', 'Independent complete-suite command definitions:', '', '```json',
              json.dumps(result['full_suite_definition'], indent=2), '```', '',
              'Baseline Git merge + CI command definitions:', '', '```json',
              json.dumps(result['baseline_suite_definition'], indent=2), '```', '',
              *['- ' + r['id'] + ': ' + str(r.get('ground_truth_definition', 'Unavailable; see error in JSON.')) for r in result['rows']]]
    lines += ['', '## Aggregate observations', '', '```json', json.dumps(result['summary'], indent=2), '```',
              '', '## Limitations', '', *['- ' + x for x in result['limitations']],
              '', 'Complete commands, outputs, environment, timing and provenance are in the sibling JSON report.']
    return '\n'.join(lines) + '\n'


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('manifest', type=pathlib.Path)
    ap.add_argument('--radar', required=True, type=pathlib.Path)
    ap.add_argument('--out', required=True, type=pathlib.Path)
    ap.add_argument('--allow-execution', action='store_true')
    ap.add_argument('--isolation-wrapper', help='JSON argv array; wraps all external runtime commands including Radar gate --run')
    ap.add_argument('--isolation-attestation', help='Describe enforced filesystem, process, credential and network boundaries')
    ap.add_argument('--timeout', type=int, default=120)
    args = ap.parse_args()
    wrapper = json.loads(args.isolation_wrapper) if args.isolation_wrapper else None
    if wrapper is not None and (not isinstance(wrapper, list) or not wrapper or not all(isinstance(a, str) for a in wrapper)):
        ap.error('--isolation-wrapper must be a nonempty JSON string array')
    manifest = json.loads(args.manifest.read_text())
    result = evaluate(manifest, args.manifest.resolve(), args.radar.resolve(), args.allow_execution, wrapper,
                      args.isolation_attestation, args.timeout)
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / 'results.json').write_text(json.dumps(result, indent=2) + '\n')
    (args.out / 'results.md').write_text(markdown(result))
    print(markdown(result))


if __name__ == '__main__':
    main()
