import test from "node:test";
import assert from "node:assert/strict";
import { contrastText, palette } from "../src/theme.js";

test("contrast", () => {
  assert.equal(contrastText(palette.surface), "#111827");
  assert.equal(contrastText(palette.primary), "#ffffff");
});
