# @radar/report

A local, dependency-free package for the stable single-repository Radar gate v1
contract. It contains TypeScript declarations and a JavaScript implementation;
no build step or framework is required. This package has not been published.

```js
import {parseReport, summarize, createSummary} from '@radar/report';
const report = parseReport(await response.text());
const view = summarize(report);
container.appendChild(createSummary(report, document));
mergeButton.disabled = !view.mergeRecommended || radarExitCode !== 0;
```

`parseReport` preserves additive fields and rejects unsupported verdicts,
statuses, policies and missing required collections. `summarize` returns an error
view for invalid input. `createSummary` uses DOM `textContent` for all report
values and creates an accessible section with checked requirements, missing
evidence and limitations. Keep the original JSON artifact available to reviewers.
The host must check subprocess exit status as well as the view; a passing static
gate covers named requirements and never establishes runtime compatibility.

Use a file dependency or workspace link to this directory during integration.
Run `npm test` or `node --test test.mjs` here. The public type declarations require
TypeScript's DOM library only when using the optional DOM summary component.
