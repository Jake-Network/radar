import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

test('reviewed backend schema retains frontend fields', () => {
    const schema = JSON.parse(readFileSync(new URL('../schema.json', import.meta.url)));
    assert.ok(schema.properties.profile.properties.email);
    assert.ok(schema.properties.display_name);
});
