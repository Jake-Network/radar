#!/usr/bin/env bash
# A live two-agent integration failure, followed by an uncommitted repair.
# Requires Git, Python 3 and a Radar binary (argument or RADAR_BIN).
set -euo pipefail
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar_bin=$(realpath "${1:-$RADAR_BIN}")
else
  echo 'Usage: examples/watch/demo.sh /path/to/radar' >&2
  exit 2
fi
python3 - "$radar_bin" <<'PY'
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading
import time

binary = sys.argv[1]
with tempfile.TemporaryDirectory(prefix='radar-watch-demo-') as directory:
    root = Path(directory)
    repo = root / 'repo'
    repo.mkdir()
    scratch = root / 'scratch'
    scratch.mkdir()
    env = dict(os.environ, RADAR_CONFIG_DIR=str(root / 'config'),
               RADAR_STATE_DIR=str(root / 'state'), TMPDIR=str(scratch),
               PYTHONDONTWRITEBYTECODE='1')
    def git(where, *args):
        return subprocess.check_output(['git', '-C', str(where), *args],
                                       env=env, stderr=subprocess.STDOUT).decode().strip()
    def put(where, name, contents):
        (where / name).write_text(contents)
    git(repo, 'init', '-q', '-b', 'main')
    git(repo, 'config', 'user.name', 'Radar watch demo')
    git(repo, 'config', 'user.email', 'demo@radar.invalid')
    git(repo, 'config', 'core.hooksPath', '/dev/null')
    put(repo, '.gitignore', '__pycache__/\n.radar/state.db*\n')
    put(repo, 'backend.py', 'def price():\n    return 1\n')
    put(repo, 'frontend.py', 'def quantity():\n    return 1\n')
    put(repo, 'test_checkout.py', '''import unittest
from backend import price
from frontend import quantity

class CheckoutTest(unittest.TestCase):
    def test_budget(self):
        self.assertLessEqual(price() * quantity(), 2)
''')
    git(repo, 'add', '.')
    git(repo, 'commit', '-qm', 'baseline')
    backend, frontend = root / 'agent-backend', root / 'agent-frontend'
    git(repo, 'worktree', 'add', '-qb', 'agent-backend', str(backend))
    git(repo, 'worktree', 'add', '-qb', 'agent-frontend', str(frontend))
    process = subprocess.Popen([binary, 'watch', '--root', str(repo), '--base', 'main',
                                '--include-dirty', '--run', '--suite', 'full',
                                '--interval', '100ms', '--debounce', '100ms', '--jsonl'],
                               env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               text=True)
    events = queue.Queue()
    captured = []
    errors = []
    def read_events():
        for line in process.stdout:
            event = json.loads(line)
            captured.append(event)
            events.put(event)
    def read_errors():
        errors.extend(process.stderr)
    readers = [threading.Thread(target=read_events, daemon=True),
               threading.Thread(target=read_errors, daemon=True)]
    for reader in readers:
        reader.start()
    def wait_report(predicate, commits=None, dirty=None):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise AssertionError('Watch exited: ' + ''.join(errors))
            try:
                event = events.get(timeout=.2)
            except queue.Empty:
                continue
            if event.get('type', event.get('Type')) != 'completed':
                continue
            result = event.get('result', event.get('Result')) or {}
            report = result.get('value', result.get('Value')) or {}
            state = event.get('state', event.get('State')) or {}
            inputs = state.get('value', state.get('Value')) or {}
            if commits is not None and set(inputs.get('commits', [])) != commits:
                continue
            snapshots = inputs.get('dirty_snapshots', [])
            has_dirty = any(s.get('staged') or s.get('unstaged') or s.get('untracked')
                            for s in snapshots)
            if dirty is not None and has_dirty != dirty:
                continue
            if predicate(report):
                return report
        raise AssertionError('Timed out waiting for watch report: ' + ''.join(errors))
    def verdict(report):
        return report.get('gate', {}).get('verdict')
    try:
        wait_report(lambda report: report.get('state') == 'NOT VERIFIED')
        first_started = time.monotonic()
        put(backend, 'backend.py', 'def price():\n    return 2\n')
        git(backend, 'commit', '-qam', 'Backend agent doubles price')
        backend_commit = git(backend, 'rev-parse', 'HEAD')
        wait_report(lambda report: verdict(report) == 'pass', {backend_commit}, False)
        first_latency = time.monotonic() - first_started
        failure_started = time.monotonic()
        put(frontend, 'frontend.py', 'def quantity():\n    return 2\n')
        git(frontend, 'commit', '-qam', 'Frontend agent doubles quantity')
        committed_inputs = {backend_commit, git(frontend, 'rev-parse', 'HEAD')}
        for checkout in [backend, frontend]:
            subprocess.run([sys.executable, '-m', 'unittest', 'test_checkout'],
                           cwd=checkout, env=env, check=True, capture_output=True)
        broken = wait_report(lambda report: verdict(report) == 'fail', committed_inputs, False)
        failure_latency = time.monotonic() - failure_started
        assert not broken.get('conflicts'), 'Expected a runtime integration failure'
        repair_started = time.monotonic()
        put(frontend, 'frontend.py', 'def quantity():\n    return 1\n')
        identities = {str(w): (git(w, 'rev-parse', 'HEAD'),
                              (w / '.git').read_bytes() if (w / '.git').is_file() else None,
                              git(w, 'status', '--porcelain')) for w in [repo, backend, frontend]}
        refs = git(repo, 'show-ref')
        index_paths = [Path(git(w, 'rev-parse', '--path-format=absolute', '--git-path', 'index'))
                       for w in [repo, backend, frontend]]
        indexes = [p.read_bytes() for p in index_paths]
        repaired = wait_report(lambda report: verdict(report) == 'pass'
                               and report.get('candidate_tree') != broken.get('candidate_tree'),
                               committed_inputs, True)
        repair_latency = time.monotonic() - repair_started
        assert git(repo, 'show-ref') == refs
        for w in [repo, backend, frontend]:
            assert git(w, 'rev-parse', 'HEAD') == identities[str(w)][0]
            assert git(w, 'status', '--porcelain') == identities[str(w)][2]
        assert [p.read_bytes() for p in index_paths] == indexes
        checks_before_idle = sum(e.get('type', e.get('Type')) == 'checking' for e in captured)
        time.sleep(.6)
        assert sum(e.get('type', e.get('Type')) == 'checking' for e in captured) == checks_before_idle
        print('PASS: each agent branch passes alone; watch observes their combined failure.')
        print('PASS: an uncommitted repair passes on a distinct temporary candidate; refs and indexes unchanged.')
        print('PASS: unchanged inputs do not start another check across multiple polling intervals.')
        print(f'Observed latency: first check {first_latency:.3f}s; failure {failure_latency:.3f}s; dirty repair {repair_latency:.3f}s.')
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGINT)
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise AssertionError('Watch did not stop after SIGINT')
        for reader in readers:
            reader.join(timeout=2)
    assert process.returncode == 0, ''.join(errors)
    sequence = [event.get('sequence', event.get('Sequence')) for event in captured]
    assert sequence == list(range(1, len(sequence) + 1)), sequence
    assert not list(scratch.iterdir()), 'Temporary watch resources leaked'
    print('PASS: JSONL sequence is contiguous; SIGINT releases temporary resources.')
PY
