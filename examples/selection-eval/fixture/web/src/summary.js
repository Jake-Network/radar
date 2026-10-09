import { formatMoney } from "./format.js";

// Renders GET /orders/{id}/summary.
export function renderSummary(summary) {
  return `Order ${summary.id}: ${formatMoney(summary.total, summary.currency)}`;
}
