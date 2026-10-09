import test from "node:test";
import assert from "node:assert/strict";
import { formatMoney } from "../src/format.js";

test("formats cents", () => assert.equal(formatMoney(12345, "USD"), "USD 123.45"));
test("pads cents", () => assert.equal(formatMoney(5, "EUR"), "EUR 0.05"));
test("negative", () => assert.equal(formatMoney(-150, "USD"), "-USD 1.50"));
