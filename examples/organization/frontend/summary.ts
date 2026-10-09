export interface ExportSummary {
  dataset_id: string;
  total: number;
}

export function renderSummary(summary: ExportSummary): string {
  return `${summary.dataset_id}: ${summary.total} rows`;
}
