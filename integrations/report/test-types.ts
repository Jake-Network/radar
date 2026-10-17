import {parseReport, summarize, createSummary, type Evidence, type GateReport, type Verdict} from './index.js';

const report: GateReport = parseReport('{}');
const order: string[] | undefined = report.landing?.order;
const related: string[] | undefined = report.conflict_brief?.files[0]?.tests;
const evidence: Evidence | undefined = report.drift?.[0]?.evidence;
const verdict: Verdict = summarize(report).verdict;
const component: HTMLElement = createSummary(report, document);
const excludedCommit: string | undefined = report.landing?.restack_first?.[0]?.commit;
void [order, related, evidence, verdict, component, excludedCommit];
// @ts-expect-error Unsupported future verdicts cannot be accepted as success.
const future: Verdict = 'success';
// @ts-expect-error Pair analysis coverage is a boolean, separate from verdict.
const complete: boolean = report.landing?.pairs[0]?.status;
void [future, complete];
