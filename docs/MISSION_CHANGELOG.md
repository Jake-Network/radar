# Reliability and adoption changes

## Verification correctness

Full selection includes explicit Node built-in test paths and nested unittest
files. Every unsupported inventoried test and every incomplete inventory
observation blocks full verification and remains inspectable. Full pytest
selection names inventoried files rather than relying solely on discovery.
Runner availability is probed, never assumed. Confirmed failures survive later
coverage and environment errors; missing required observations cannot pass.

Validation results and platform limits are recorded in MISSION_VALIDATION.md.

## Pull-request integration

Replaced placeholder installation and all-open-PR scanning with immutable source
installation, explicit selected inputs, candidate verification, owner-controlled
execution, escaped annotations, bounded job summaries and JSON artifacts. Local
scenario tests include separately passing changes with observed combined failure.

## Contract discovery

Replaced the regex-assisted same-file discovery path with bounded Tree-sitter
Python model/router resolution and TypeScript import/export/response resolution.
Added imported/inherited/nested models, literal router registration prefixes,
type aliases/barrels, typed fetch and Axios instances, lexical scope handling,
negative regressions and a representative runnable snapshot-test fixture.
Discovery never writes accepted contracts or claims runtime schema certainty.

## Native distribution

Native-host packaging now includes Windows amd64 ZIP alongside Unix tarballs,
version-matched binaries, normalized archives, dependency notices and checksums.
Added installed-archive parser smoke and safe PowerShell installation with native
Windows regressions. Extended Unix malicious-archive regression coverage. All
native CI tests are required; tag pushes prepare assets and publication requires
an explicit manual protected-environment request. No automatic tap pushes.
