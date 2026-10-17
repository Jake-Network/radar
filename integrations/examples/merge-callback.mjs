/** Local orchestrator adapter. Install Radar first; this example never merges. */
import {spawnSync} from 'node:child_process';
import {summarize} from '../report/index.js';

export function inspectBeforeMerge({root, base, branches, radar = 'radar'}) {
  if (!Array.isArray(branches) || !branches.length || branches.some(b => typeof b !== 'string' || !b || b.startsWith('-')) ||
      typeof base !== 'string' || !base || base.startsWith('-')) throw new TypeError('Explicit base and branch refs required.');
  const result = spawnSync(radar, ['gate', ...branches, '--base', base, '--json'], {
    cwd: root, encoding: 'utf8', timeout: 120000, maxBuffer: 4 * 1024 * 1024,
    shell: false,
  });
  const view = summarize(result.stdout);
  return {...view, mergeRecommended: result.status === 0 && !result.error && view.mergeRecommended,
    exitCode: result.status, rawReport: result.stdout};
}
