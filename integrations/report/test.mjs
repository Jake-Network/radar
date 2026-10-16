import test from 'node:test';
import assert from 'node:assert/strict';
import {parseReport, summarize, createSummary} from './index.js';
const report = () => ({gate:{verdict:'pass', policy:{version:1, require:['textual_merge']}, required_checks:[{id:'textual_merge', status:'passed', evidence:'verified_static', explanation:'combined'}], explanation:'selected policy'},status:'passed',base:'main',inputs:['agent-a'],checks:[],coverage:[],findings:[],diagnostics:[],limitations:['Static scope only.']});
test('valid v1 preserves additive evidence and separate status', () => {
  const r = {...report(), status:'incomplete', future:{x:1}};
  assert.equal(parseReport(r), r); assert.equal(summarize(r).mergeRecommended, true);
  assert.equal(parseReport(JSON.stringify(r)).status, 'incomplete');
});
test('unknown verdicts, missing requirements and malformed data fail closed', () => {
  for (const r of [null, '{', {}, {...report(), gate:{...report().gate,verdict:'future'}}, {...report(),gate:{...report().gate,required_checks:[]}}, {...report(),status:'green'}]) {
    assert.equal(summarize(r).mergeRecommended, false);
    assert.equal(summarize(r).verdict, 'error');
  }
});
test('blocked is never a merge recommendation', () => {
  const r=report(); r.gate.verdict='blocked'; r.gate.required_checks[0].status='unknown';
  assert.equal(summarize(r).mergeRecommended,false); assert.equal(summarize(r).notVerified.length,1);
});
test('static PASS does not hide failed integration ref publication', () => {
  const r={...report(),landing_apply:{published:false,objects_imported:true,reason:'inputs changed before publication'}};
  const view=summarize(r);
  assert.equal(view.verdict,'pass');
  assert.equal(view.mergeRecommended,false);
  assert.match(view.title,/not published/);
  assert.ok(view.notVerified.some(s=>s.includes(r.landing_apply.reason)&&s.includes('true')));
  assert.equal(summarize({...r,landing_apply:{...r.landing_apply,published:'false'}}).verdict,'error');
});
test('component renders untrusted strings exclusively as text', () => {
  const nodes=[]; const document={createElement(tag){ const node={tag, children:[],setAttribute(){},appendChild(child){this.children.push(child);},set innerHTML(_){throw Error('unsafe HTML');}};nodes.push(node);return node;}};
  const r=report(); r.limitations=['<img src=x onerror=alert(1)>'];
  const section=createSummary(r,document);
  assert.equal(section.tag,'section'); assert.ok(nodes.some(n=>n.textContent===r.limitations[0]));
});
test('current public single-repo golden reports satisfy the consumer contract', async () => {
  const {readFile, readdir} = await import('node:fs/promises');
  const dir = new URL('../../internal/cli/testdata/single-repo/', import.meta.url);
  for (const name of (await readdir(dir)).filter(name => name.endsWith('.json.golden'))) {
    const input = JSON.parse((await readFile(new URL(name, dir), 'utf8')).replace(/^exit \d+\n/, ''));
    // CLI setup errors may intentionally have no report object.
    if (input.gate) assert.equal(parseReport(input).gate.verdict, input.gate.verdict, name);
  }
});

test('restack exclusions survive rendering and cannot recommend a full merge', () => {
  const excluded = {name:'needs-restack', commit:'a'.repeat(40), conflicts:['api.py']};
  const r = {...report(), landing:{restack_first:[excluded]}};
  assert.equal(summarize(r).mergeRecommended, false);
  assert.throws(() => parseReport(r), /excludes selected landing inputs/);
  r.gate.verdict = 'blocked';
  assert.deepEqual(parseReport(r).landing.restack_first, [excluded]);
  assert.ok(summarize(r).notVerified.some(row => row.includes('needs-restack')));
  for (const invalid of [null, {}, [{...excluded, behind:-1}], [{...excluded, conflicts:'api.py'}]]) {
    assert.equal(summarize({...r, landing:{restack_first:invalid}}).verdict, 'error');
  }
  assert.equal(summarize({...report(), landing:{restack_first:[]}}).mergeRecommended, true);
});
