#!/usr/bin/env python3
"""Explicit, bounded public Git acquisition for the original 400-request sample.

Uses authenticated read-only `gh api` for public PR metadata, and anonymous
HTTPS Git fetches for immutable objects. Never executes repository code. This
helper is separate from the Radar CLI and never substitutes sampled requests.
"""
import argparse
import concurrent.futures
import configparser
import contextlib
from datetime import datetime, timezone
import hashlib
import json
import os
import pathlib
import signal
import subprocess
import threading
import time

import dataset
import replay
import sampling

SAFE = [*replay.SAFE, '-c', 'core.hooksPath=/dev/null', '-c', 'credential.helper=',
        '-c', 'protocol.file.allow=never', '-c', 'protocol.ext.allow=never',
        '-c', 'fetch.fsckObjects=true', '-c', 'core.autocrlf=false']
FETCH_FLAGS = ('--no-tags', '--filter=blob:none', '--no-auto-maintenance',
               '--no-recurse-submodules', '--no-write-commit-graph')


class Unavailable(Exception):
    pass


def historical_anchors(request, pr, merge):
    """Two-parent merge preserves both historical integration inputs immutably.

    Squash/rebase/open PRs do not supply this proof; current target tips are
    never substituted. Requiring the recorded PR head to match the merge parent
    also refuses post-merge head movement rather than guessing.
    """
    if not pr.get('merged') or not pr.get('merged_at'):
        raise Unavailable('pr_not_merged')
    parents = [parent.get('sha') for parent in merge.get('parents', [])]
    if len(parents) != 2:
        raise Unavailable('historical_two_parent_anchor_unavailable')
    head = pr.get('head', {}).get('sha')
    commit = pr.get('merge_commit_sha')
    if merge.get('sha') != commit or parents[1] != head:
        raise Unavailable('historical_merge_head_mismatch')
    if any(not isinstance(sha, str) or not sampling.SHA.fullmatch(sha) for sha in [commit, *parents]):
        raise Unavailable('invalid_immutable_anchor')
    return {'base_oid': parents[0], 'head_oid': parents[1], 'merge_commit': commit,
            'anchor_evidence': 'GitHub PR ' + request['url'] + '; immutable two-parent merge ' + commit,
            'merged_at': pr['merged_at'], 'method': 'github_two_parent_merge'}


def public_api(endpoint):
    p = subprocess.run(['gh', 'api', '--method', 'GET', endpoint], capture_output=True, timeout=45)
    if p.returncode:
        # No credential, stderr, private metadata or raw payload is persisted.
        raise Unavailable('github_api_unavailable')
    body = p.stdout
    data = json.loads(body)
    if not isinstance(data, dict):
        raise Unavailable('github_api_invalid_payload')
    return data, hashlib.sha256(body).hexdigest()


def byte_size(path):
    total = 0
    for root, _, files in os.walk(path):
        for name in files:
            try:
                total += (pathlib.Path(root) / name).stat().st_size
            except FileNotFoundError:
                pass
    return total


class Acquirer:
    def __init__(self, directory, timeout=90, repo_bytes=256 * 1024**2, total_bytes=8192 * 1024**2):
        self.directory = pathlib.Path(directory).resolve()
        self.directory.mkdir(parents=True, exist_ok=True)
        self.timeout, self.repo_bytes, self.total_bytes = timeout, repo_bytes, total_bytes
        self.locks, self.lock = {}, threading.Lock()
        self.local = threading.local()
        self.git_env = dict(replay.GIT_ENV, GIT_ASKPASS='/bin/false', SSH_ASKPASS='/bin/false')
        self.size_cache, self.size_lock = {}, threading.Lock()

    def size(self, path):
        # Cache monitor scans across workers; repeated full-directory walks can
        # otherwise dominate even tiny Git commands as the corpus grows.
        with self.size_lock:
            previous = self.size_cache.get(str(path))
            if previous and time.monotonic() - previous[0] < 1.0:
                return previous[1]
            value = byte_size(path)
            self.size_cache[str(path)] = (time.monotonic(), value)
            return value

    def git(self, repo, *args, input=None):
        """Kill a fetch that exceeds elapsed time or the on-disk object budget."""
        started = time.monotonic()
        with tempfile_output() as stdout, tempfile_output() as stderr:
            p = subprocess.Popen(['git', '-C', str(repo), *SAFE, *args], env=self.git_env,
                                 stdin=subprocess.PIPE if input is not None else subprocess.DEVNULL,
                                 stdout=stdout, stderr=stderr, start_new_session=True)
            if input is not None:
                p.stdin.write(input.encode())
                p.stdin.close()
            while p.poll() is None:
                reason = None
                if time.monotonic() - started > self.timeout or time.monotonic() > getattr(self.local, 'deadline', float('inf')):
                    reason = 'git_timeout'
                elif self.size(repo) > self.repo_bytes:
                    reason = 'repository_disk_budget'
                elif self.size(self.directory) > self.total_bytes:
                    reason = 'total_disk_budget'
                if reason:
                    os.killpg(p.pid, signal.SIGKILL)
                    p.wait()
                    raise Unavailable(reason)
                time.sleep(.1)
            if time.monotonic() > getattr(self.local, 'deadline', float('inf')):
                raise Unavailable('git_timeout')
            if args[0] in ('fetch', 'checkout') and byte_size(repo) > self.repo_bytes:
                raise Unavailable('repository_disk_budget')
            stdout.seek(0)
            body = stdout.read()
            if p.returncode:
                stderr.seek(0)
                error = stderr.read()
                self.local.failure = {'operation': args[0], 'exit_code': p.returncode,
                                      'stderr_sha256': hashlib.sha256(error).hexdigest()}
                message = error.decode('utf-8', errors='replace').lower()
                reason = 'git_command_unavailable'
                if 'not our ref' in message or 'unadvertised object' in message:
                    reason = 'git_object_unavailable'
                elif 'could not read username' in message or 'authentication failed' in message or 'repository not found' in message:
                    reason = 'git_repository_access_unavailable'
                elif '.lock' in message and 'file exists' in message:
                    reason = 'git_cache_locked'
                elif 'rpc failed' in message or 'remote end hung up' in message or 'http/2' in message:
                    reason = 'git_transport_unavailable'
                raise Unavailable(reason)
            return body.decode('utf-8')

    def validate_cache(self, repo, expected_url):
        # Cached repositories are data, not trusted executable configuration.
        gitdir = repo / '.git'
        if repo.is_symlink() or gitdir.is_symlink() or not gitdir.is_dir():
            raise Unavailable('unsafe_repository_cache')
        if hasattr(os, 'getuid') and gitdir.stat().st_uid != os.getuid():
            raise Unavailable('unsafe_repository_cache')
        for directory, subdirs, files in os.walk(gitdir, followlinks=False):
            if any((pathlib.Path(directory) / name).is_symlink() for name in [*subdirs, *files]):
                raise Unavailable('unsafe_repository_cache')
        for relative in ('commondir', 'gitdir', 'config.worktree', 'objects/info/alternates'):
            if (gitdir / relative).exists() or (gitdir / relative).is_symlink():
                raise Unavailable('unsafe_repository_cache')
        config = gitdir / 'config'
        if config.is_symlink() or not config.is_file():
            raise Unavailable('unsafe_repository_cache')
        allowed = {'core': {'repositoryformatversion', 'filemode', 'bare', 'logallrefupdates'},
                   'remote "origin"': {'url', 'fetch', 'promisor', 'partialclonefilter'},
                   'extensions': {'partialclone', 'objectformat'}}
        parsed = configparser.ConfigParser(interpolation=None, strict=True)
        try:
            parsed.read_string(config.read_text())
            for section in parsed.sections():
                if section not in allowed or not set(parsed[section]).issubset(allowed[section]):
                    raise Unavailable('unsafe_repository_configuration')
            if parsed.get('remote "origin"', 'url', fallback='') != expected_url:
                raise Unavailable('repository_origin_mismatch')
        except configparser.Error:
            raise Unavailable('unsafe_repository_configuration')

    def one(self, request):
        key = request['source'] + ':' + request['source_id']
        row = {'id': key, 'source': request['source'], 'source_id': request['source_id'],
               'repository_url': request['repository_url'], 'status': 'unavailable',
               'retrieved_at': datetime.now(timezone.utc).isoformat()}
        started = time.monotonic()
        self.local.deadline = started + self.timeout
        self.local.failure = None
        try:
            slug, number = request['source_id'].rsplit('#', 1)
            if not sampling.SLUG.fullmatch(slug) or not number.isdigit():
                raise Unavailable('invalid_public_repository')
            if request['repository_url'] != 'https://github.com/' + slug:
                raise Unavailable('invalid_public_repository')
            if request['source'] == 'agenticflict':
                anchors = {k: request[k] for k in ('base_oid', 'head_oid')}
                anchors.update(method='pinned_agenticflict_csv', anchor_evidence='AgenticFlict source lock and original sample input SHAs')
            else:
                pr, pr_digest = public_api('repos/' + slug + '/pulls/' + number)
                row['api_provenance'] = {'pr_url': 'https://api.github.com/repos/' + slug + '/pulls/' + number,
                                         'pr_payload_sha256': pr_digest}
                if not pr.get('merged'):
                    raise Unavailable('pr_not_merged')
                if pr.get('base', {}).get('repo', {}).get('private') is not False:
                    raise Unavailable('repository_not_confirmed_public')
                merge_sha = pr.get('merge_commit_sha', '')
                if not isinstance(merge_sha, str) or not sampling.SHA.fullmatch(merge_sha):
                    raise Unavailable('invalid_immutable_anchor')
                merge, merge_digest = public_api('repos/' + slug + '/commits/' + merge_sha)
                row['api_provenance'].update(merge_url='https://api.github.com/repos/' + slug + '/commits/' + merge_sha,
                                             merge_payload_sha256=merge_digest)
                anchors = historical_anchors(request, pr, merge)
            for name in ('base_oid', 'head_oid'):
                if not sampling.SHA.fullmatch(anchors[name]):
                    raise Unavailable('invalid_immutable_anchor')
            row['anchors'] = anchors
            repository_key = hashlib.sha256(request['repository_url'].encode()).hexdigest()[:24]
            repo = self.directory / repository_key
            with self.lock:
                lock = self.locks.setdefault(repository_key, threading.Lock())
            with deadline_lock(lock, self.local.deadline):
                if self.size(self.directory) >= self.total_bytes:
                    raise Unavailable('total_disk_budget')
                if not (repo / '.git').exists():
                    repo.mkdir(exist_ok=True)
                    self.git(repo, 'init', '--template=')
                    self.git(repo, 'remote', 'add', 'origin', request['repository_url'])
                self.validate_cache(repo, request['repository_url'])
                if byte_size(repo) > self.repo_bytes:
                    raise Unavailable('repository_disk_budget')
                base, head = anchors['base_oid'], anchors['head_oid']
                # Complete history for root identity; only blobs are filtered.
                self.git(repo, 'fetch', *FETCH_FLAGS, 'origin', base, head)
                ancestor = self.git(repo, 'merge-base', base, head).strip()
                refs = [base, head, ancestor]
                for ref in refs:
                    if not sampling.SHA.fullmatch(ref):
                        raise Unavailable('merge_base_unavailable')
                    tree = self.git(repo, 'ls-tree', '-r', '--name-only', ref)
                    if len(tree.splitlines()) > 10000:
                        raise Unavailable('tracked_file_limit')
                # --no-walk enumerates these commit trees, never history blobs.
                missing = self.git(repo, 'rev-list', '--objects', '--missing=print', '--no-walk', *refs)
                object_ids = sorted({line[1:] for line in missing.splitlines() if line.startswith('?')})
                for i in range(0, len(object_ids), 128):
                    # Negotiate no haves for blob hydration: a thin pack can
                    # otherwise require an absent promisor delta base and try
                    # an implicit fetch, which is deliberately disabled.
                    self.git(repo, 'fetch', '--refetch', *FETCH_FLAGS, 'origin', *object_ids[i:i + 128])
                remaining = self.git(repo, 'rev-list', '--objects', '--missing=print', '--no-walk', *refs)
                if any(line.startswith('?') for line in remaining.splitlines()):
                    raise Unavailable('immutable_tree_objects_unavailable')
                # Local refs retain every accepted pair for resumable acquisition.
                suffix = hashlib.sha256(key.encode()).hexdigest()[:20]
                self.git(repo, 'update-ref', 'refs/heads/inputs/' + suffix + '-base', base)
                self.git(repo, 'update-ref', 'refs/heads/inputs/' + suffix + '-head', head)
                # One bounded checkout makes the repository valid for static replay.
                # Fresh config has no filters/merge drivers and hooks are disabled.
                if not (repo / '.git/acceptance-checkout').exists():
                    self.git(repo, 'checkout', '--detach', base)
                    (repo / '.git/acceptance-checkout').write_text(base + '\n')
                # Uncached final checks cover fast operations between monitor polls.
                if time.monotonic() > self.local.deadline:
                    raise Unavailable('git_timeout')
                if byte_size(repo) > self.repo_bytes:
                    raise Unavailable('repository_disk_budget')
                if byte_size(self.directory) > self.total_bytes:
                    raise Unavailable('total_disk_budget')
                row.update(status='acquired', hydrated_blob_objects=len(object_ids),
                           object_bytes=byte_size(repo / '.git'),
                           trees={ref: self.git(repo, 'rev-parse', ref + '^{tree}').strip() for ref in refs},
                           roots=sorted(set(self.git(repo, 'rev-list', '--max-parents=0', base, head).split())),
                           repository_key=repository_key)
                return row, dict(anchors, repo=str(repo))
        except Unavailable as error:
            row['reason'] = str(error)
            if self.local.failure:
                row['git_failure'] = self.local.failure
        except (OSError, ValueError, subprocess.SubprocessError) as error:
            row['reason'] = type(error).__name__
        finally:
            row['seconds'] = round(time.monotonic() - started, 3)
        return row, None



@contextlib.contextmanager
def deadline_lock(lock, deadline):
    if not lock.acquire(timeout=max(0, deadline - time.monotonic())):
        raise Unavailable('git_timeout')
    try:
        yield
    finally:
        lock.release()


def tempfile_output():
    import tempfile
    return tempfile.TemporaryFile()


def atomic_write(path, value):
    temporary = pathlib.Path(str(path) + '.tmp')
    temporary.write_text(json.dumps(value, sort_keys=True, indent=2) + '\n')
    temporary.replace(path)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('sample')
    parser.add_argument('directory')
    parser.add_argument('--jobs', type=int, default=4)
    parser.add_argument('--timeout', type=int, default=90)
    parser.add_argument('--repo-mib', type=int, default=256)
    parser.add_argument('--total-mib', type=int, default=8192)
    parser.add_argument('--limit', type=int, default=400)
    args = parser.parse_args(argv)
    if not (1 <= args.jobs <= 8 and 1 <= args.limit <= 400 and args.timeout > 0 and args.repo_mib > 0 and args.total_mib > 0):
        parser.error('jobs 1..8, limit 1..400 and positive budgets required')
    sampled = sampling.read_json(args.sample)
    sampling.validate_sample(sampled)
    acquirer = Acquirer(args.directory, args.timeout, args.repo_mib * 1024**2, args.total_mib * 1024**2)
    rows, inputs = [], {}
    previous_digest = None
    tool_digest = sampling.file_digest(__file__)
    git_version = subprocess.check_output(['git', '--version'], text=True).strip()
    root = pathlib.Path(args.directory)
    if (root / 'acquisition.json').exists():
        previous = sampling.read_json(root / 'acquisition.json')
        if previous.get('sample_digest') != sampled['sample_digest']:
            parser.error('resume sample digest mismatch')
        previous_inputs = sampling.read_json(root / 'local-inputs.json')
        previous_digest = dataset.digest(previous)
        history = root / 'attempt-history'
        history.mkdir(exist_ok=True)
        atomic_write(history / (previous_digest + '.json'), previous)
        # No acquisition worker runs during this loop. Scan the shared total
        # before/after it rather than re-walking every repository per cache row.
        resume_total_bytes = byte_size(root)
        # Revalidate cached configuration and uncached actual sizes before reuse.
        # Unavailable attempts remain retryable; historical manifests remain intact.
        for row in previous['requests']:
            if row['status'] != 'acquired' or row['id'] not in previous_inputs:
                continue
            local = previous_inputs[row['id']]
            expected = root.resolve() / hashlib.sha256(row['repository_url'].encode()).hexdigest()[:24]
            if pathlib.Path(local['repo']) != expected:
                continue
            try:
                acquirer.validate_cache(expected, row['repository_url'])
                if byte_size(expected) > acquirer.repo_bytes or resume_total_bytes > acquirer.total_bytes:
                    continue
            except Unavailable:
                continue
            rows.append(row)
            inputs[row['id']] = local
        if byte_size(root) > acquirer.total_bytes:
            rows, inputs = [], {}
    completed = set(inputs)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        futures = {pool.submit(acquirer.one, request): request for request in sampled['requests'][:args.limit]
                   if request['source'] + ':' + request['source_id'] not in completed}
        for future in concurrent.futures.as_completed(futures):
            row, local = future.result()
            rows.append(row)
            if local:
                inputs[row['id']] = local
            rows.sort(key=lambda row: row['id'])
            atomic_write(root / 'local-inputs.json', inputs)
            report = {'schema_version': 1, 'sample_digest': sampled['sample_digest'],
                      'target_requests': sampled['target_cases'], 'attempted': len(rows),
                      'acquired': len(inputs), 'budget': vars(args) | {'sample': sampled['sample_digest'], 'directory': 'local only'},
                      'previous_report_digest': previous_digest,
                      'tool_sha256': tool_digest, 'git_version': git_version,
                      'requests': rows}
            atomic_write(root / 'acquisition.json', report)
            print(row['id'], row['status'], row.get('reason', ''), row['seconds'], flush=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
