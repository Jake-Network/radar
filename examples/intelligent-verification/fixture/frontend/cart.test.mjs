import test from 'node:test';
import assert from 'node:assert/strict';
import { quantity } from './cart.mjs';

test('supported cart quantity', () => {
  assert.ok(Number.isInteger(quantity));
  assert.ok(quantity >= 1 && quantity <= 2);
});
