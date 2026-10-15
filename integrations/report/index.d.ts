/** Matches schemas/gate-report.schema.json v1. Additional evidence is additive. */
export type Verdict = 'pass' | 'fail' | 'blocked' | 'error';
export type Status = 'passed' | 'failed' | 'unknown' | 'incomplete' | 'blocked' | 'warning' | 'error' | 'timeout';
export type Evidence = 'verified_static' | 'verified_tool' | 'observed_test' | 'inferred' | 'proposed' | 'unknown';
export interface Check { id: string; status: Status; evidence: Evidence; explanation: string; [field: string]: unknown }
export interface Location { repository: string; revision: string; path?: string; line?: number; end_line?: number; method: string; evidence: Evidence }
export interface Finding { id: string; code: string; severity: 'error' | 'warning' | 'info'; evidence: Evidence; explanation: string; branches?: string[]; locations?: Location[]; remediation?: string; [field: string]: unknown }
export interface LandingPair { left: string; right: string; status: Status; checks: Check[]; findings: Finding[]; conflicts: string[]; explanation: string; resolved_by?: string; resolved_files?: string[] }
export interface LandingReport { base: string; driver?: string; branches: {name: string; commit: string; changed: string[]}[]; pairs: LandingPair[]; order: string[]; independent: string[]; chains: string[][]; complete: boolean; estimated_rebases: number; order_method: string; limitations: string[] }
export interface ConflictBrief { base: string; left: string; right: string; files: {path: string; base: string; left: string; right: string; hunks: {left: string; right: string}[]; symbols: string[]; tests: string[]; complete: boolean}[]; reproduction: string[]; evidence: 'inferred'; limitations: string[] }
export interface LandingApplyOutcome { published: boolean; objects_imported: boolean; reason?: string }
export interface GateReport {
  gate: { verdict: Verdict; policy: {version: 1; name?: string; require: string[]; on_missing?: 'blocked' | 'fail'}; required_checks: Check[]; explanation: string; [field: string]: unknown };
  status: Status; base: string; inputs: string[]; checks: Check[];
  coverage: Record<string, unknown>[]; findings: Finding[];
  diagnostics: Record<string, unknown>[]; limitations: string[];
  executions?: Record<string, unknown>[]; generated_executions?: Record<string, unknown>[];
  runner_preparations?: Record<string, unknown>[];
  /** Optional versioned roadmap evidence; consumers must inspect its own status. */
  landing?: LandingReport; conflict_brief?: ConflictBrief; landing_apply?: LandingApplyOutcome;
  drift?: Finding[]; overlap?: {drift: Finding[]; pairs_checked: number; limitations: string[]};
  integration_ref?: {ref: string; commit: string; tree: string; fast_forward: string[]; report: GateReport; outcome?: LandingApplyOutcome}; run_id?: string;
  [field: string]: unknown;
}
export interface Summary { verdict: Verdict; mergeRecommended: boolean; title: string; checked: string[]; notVerified: string[]; limitations: string[]; execution: string }
export function parseReport(input: unknown): GateReport;
export function summarize(input: unknown): Summary;
export function createSummary(input: unknown, document: Document): HTMLElement;
