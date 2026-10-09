import test from "node:test";
import assert from "node:assert/strict";
import { renderSummary } from "../src/summary.js";

test("renders summary", () => {
  assert.equal(renderSummary({ id: "o-1", total: 2025, currency: "USD", items: 3 }), "Order o-1: USD 20.25");
});
