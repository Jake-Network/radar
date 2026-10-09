import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { renderSummary } from "../src/summary.js";

// The storefront must render every schema-valid summary.
const schema = JSON.parse(readFileSync(new URL("../../contracts/order-summary.schema.json", import.meta.url)));

test("renders a schema example", () => {
  const example = { id: "o-2", items: 1 };
  for (const field of schema.required) if (!(field in example)) example[field] = schema.properties[field].type === "integer" ? 100 : "USD";
  assert.match(renderSummary(example), /^Order o-2: USD 1\.00$/);
});
