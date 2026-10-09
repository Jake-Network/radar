import { renderSummary, type ExportSummary } from "./summary";

export async function showDatasetSummary(url: string): Promise<string> {
  const response = await fetch(url);
  const summary: ExportSummary = await response.json();
  return renderSummary(summary);
}
