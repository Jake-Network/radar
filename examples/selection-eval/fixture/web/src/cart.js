import { formatMoney } from "./format.js";

export function cartLabel(lines, currency) {
  const cents = lines.reduce((sum, [unit, qty]) => sum + unit * qty, 0);
  return `${lines.length} lines, ${formatMoney(cents, currency)}`;
}
