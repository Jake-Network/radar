import { test, expect } from 'vitest';
import { priceLabel } from './view';
test('price', () => expect(priceLabel(100)).toBe('100'));
