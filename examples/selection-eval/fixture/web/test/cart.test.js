import test from "node:test";
import assert from "node:assert/strict";
import { cartLabel } from "../src/cart.js";

test("cart label", () => assert.equal(cartLabel([[100, 2], [50, 1]], "USD"), "2 lines, USD 2.50"));
