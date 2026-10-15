/** Radar JSON is untrusted input. Preserve unknown fields and fail closed. */
const verdicts = new Set(['pass', 'fail', 'blocked', 'error']);
const statuses = new Set(['passed', 'failed', 'unknown', 'incomplete', 'blocked', 'warning', 'error', 'timeout']);
const evidence = new Set(['verified_static', 'verified_tool', 'observed_test', 'inferred', 'proposed', 'unknown']);
export function parseReport(input) {
  const report = typeof input === 'string' ? JSON.parse(input) : input;
  if (!report || typeof report !== 'object' || Array.isArray(report)) throw new TypeError('Expected a Radar gate report object.');
  if (!report.gate || !verdicts.has(report.gate.verdict) || !statuses.has(report.status) ||
      typeof report.base !== 'string' || !Array.isArray(report.inputs) || !report.inputs.every(x => typeof x === 'string') ||
      !Array.isArray(report.checks) || !Array.isArray(report.gate.required_checks) ||
      !Array.isArray(report.coverage) || !Array.isArray(report.findings) ||
      !Array.isArray(report.diagnostics) || !Array.isArray(report.limitations)) {
    throw new TypeError('Missing or unsupported Radar v1 verdict, status, or evidence collections.');
  }
  const policy = report.gate.policy;
  if (!policy || policy.version !== 1 || !Array.isArray(policy.require) || !policy.require.length ||
      !policy.require.every(x => typeof x === 'string' && x) || new Set(policy.require).size !== policy.require.length ||
      typeof report.gate.explanation !== 'string') throw new TypeError('Unsupported or missing Radar policy.');
  for (const check of [...report.checks, ...report.gate.required_checks]) {
    if (!check || typeof check.id !== 'string' || !check.id || !statuses.has(check.status) ||
        !evidence.has(check.evidence) || typeof check.explanation !== 'string') throw new TypeError('Unsupported Radar check.');
  }
  if (report.gate.verdict === 'pass' && policy.require.some(id =>
      !report.gate.required_checks.some(c => c.id === id && c.status === 'passed'))) {
    throw new TypeError('A passing report is missing a passed required check.');
  }
  if (report.landing_apply !== undefined) {
    const outcome = report.landing_apply;
    if (!outcome || typeof outcome !== 'object' || typeof outcome.published !== 'boolean' ||
        typeof outcome.objects_imported !== 'boolean' || (outcome.reason !== undefined && typeof outcome.reason !== 'string')) {
      throw new TypeError('Unsupported landing publication outcome.');
    }
  }
  return report;
}

/** A view model, not an authorization to mutate refs or run repository code. */
export function summarize(input) {
  try {
    const report = parseReport(input);
    const unpublished = report.landing_apply?.published === false;
    return {
      verdict: report.gate.verdict,
      mergeRecommended: report.gate.verdict === 'pass' && !unpublished,
      title: `Radar: ${report.gate.verdict}${unpublished ? '; integration ref not published' : ''}`,
      checked: report.gate.required_checks.filter(c => c.status === 'passed').map(c => `${c.id}: ${c.explanation}`),
      notVerified: [
        ...report.gate.required_checks.filter(c => c.status !== 'passed').map(c => `${c.id} (${c.status}): ${c.explanation}`),
        ...(unpublished ? [`Integration ref not published: ${report.landing_apply.reason || 'publication unavailable'}. Completed object import: ${report.landing_apply.objects_imported}.`] : []),
      ],
      limitations: report.limitations.map(String),
      execution: Array.isArray(report.executions) && report.executions.length ? 'Inspect individual observations; static checks do not establish runtime compatibility.' : 'Repository tests were not executed.',
    };
  } catch {
    return {verdict: 'error', mergeRecommended: false, title: 'Radar: verification unavailable', checked: [],
      notVerified: ['Missing, malformed, or unsupported report; rerun Radar.'], limitations: [], execution: 'Runtime compatibility remains unverified.'};
  }
}

/** Uses textContent only. Safe with report strings containing HTML or markdown. */
export function createSummary(input, document) {
  const view = summarize(input);
  const section = document.createElement('section');
  section.setAttribute('aria-label', 'Radar integration verification');
  const heading = document.createElement('h2');
  heading.textContent = view.title;
  section.appendChild(heading);
  for (const [title, rows] of [['Checked', view.checked], ['Not verified', view.notVerified], ['Limitations', view.limitations]]) {
    const h = document.createElement('h3'); h.textContent = title; section.appendChild(h);
    const list = document.createElement('ul');
    for (const text of rows.length ? rows : ['None recorded; inspect the full report scope.']) {
      const item = document.createElement('li'); item.textContent = text; list.appendChild(item);
    }
    section.appendChild(list);
  }
  const execution = document.createElement('p'); execution.textContent = view.execution; section.appendChild(execution);
  return section;
}
