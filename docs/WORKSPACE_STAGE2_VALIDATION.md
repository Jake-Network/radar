# Workspace 2단계 검증 기록

2026-10-10. 작업 브랜치: `workspace-stage2`, 기준: `workspace-stage1`의 `8bbac90`. 시작 시 PR #5는 OPEN이었으므로 stacked local branch로 구현했다. 원격 push/PR 게시/머지는 수행하지 않았다.

## 구현 결과와 커밋

| 커밋 | 내용 |
| --- | --- |
| `fd2b4d5` | 미연결 typed consumer와 literal endpoint/method 기반 repo 간 제안 |
| `ba32310` | request/response 방향과 4칸 연결 판정 엔진 |
| `c842e5d` | 팀 파일·소비 선언·producer 주소 검증, home과 팀 범위 |
| `dcb4913` | README, 제품 사양, Codex/Claude skill 근거 범위 |
| `0d5c6aa` | 손상된 기준 선언과 부분 retirement로 의무를 지우는 PASS 방지 |
| `5aebfc8` | gate 판정·텍스트·JSON·스키마·MCP·연결 회귀 테스트 |
| `eeda1a8` | worktree 지정 connect, 두 파일 보존·동시 쓰기, show, 재현 데모 |
| `49cb2f4` | 커밋/팀 blob 고정, 후보 파일 캐시, digest와 replay |
| `a2311bd` | 제외된 레지스트리 repo와 긴 기본 ID 회귀 테스트 |
| `f73cb89` | 같은 repo 쌍의 다른 producer 문서가 기본 ID로 서로 덮어쓰지 않게 함 |

## 확정한 선택과 계획 대비 차이

- 계획서 4.1의 6개 질문에 사용자가 모두 추천안을 선택했다. 첫 connect의 현재 repo를 home으로 기록하고, 정확히 하나의 커밋된 팀 파일을 보조 탐색한다. repo 식별은 root commit 집합이며 report/registry 형식은 v1을 유지한다. connect의 direction은 필수이고 알 수 없는 필드는 오류다. source는 선택 플래그다.
- 후보를 열어두는 대신 필요한 문서, 선언, source 존재 정보와 discovery report를 Close 전에 메모리에 저장한다. 후보에서 추가한 연결의 문서도 읽도록 home 후보를 먼저 구성하고, 출력은 원래 repo 순서를 유지한다.
- replay는 기록된 home 기준 커밋과 blob OID를 확인한다. home이 `--only` 범위 밖이면 snapshot의 home locator로 선언을 읽으며, 별도로 선택되지 않은 repo 후보를 검사했다고 주장하지 않는다. 기록된 커밋 객체가 없어지면 재현할 수 없다.
- 제안은 소비 필드 수 내림차순, producer/consumer/후보 ID 순으로 정렬한다. 텍스트와 MCP는 3개, JSON은 전부 담는다. JSON/YAML producer 경로와 필드가 있는 제안만 확정 명령과 함께 텍스트에 표시한다. 제안은 판정에 들어가지 않는다.
- 방향이 없어 경고만 관측되는 수동 선언도 CLI에서는 보수적으로 NOT VERIFIED로 남긴다.
- 작은 변경을 독립 구현·검증해 커밋 순서는 계획의 선형 순서와 다르다. 전체 통합 검증은 최종 코드에서 수행했다.
- connect는 하나의 registry lock 아래 두 선언의 read/modify/write를 직렬화한다. 파일별 교체는 원자적이나 두 repo와 registry를 묶는 crash-safe transaction은 아니다. 두 번째 파일 쓰기 오류에는 첫 파일을 복구한다. 두 파일을 쓴 뒤 registry 저장에 실패하면 이미 쓴 경로를 ERROR에 명시한다.
- 기본 link ID는 repo 쌍·방향 이름과 producer 주소/consumer/direction의 hash를 사용해 64자 이내로 만든다. 같은 repo 쌍의 다른 문서·pointer를 별개 연결로 보존하고 같은 연결의 재실행은 갱신한다.

## 계획서 6절 대비 테스트

| 범위 | 검증 근거 |
| --- | --- |
| home 기준 파일, 미커밋 무시·경고, pinned blob replay | composition `TestTeamCommittedSnapshotAndReplay`, CLI `TestWorkspaceStage2LinksAndReplay`, `TestWorkspaceStage2PartialAndUncommittedTeam` |
| orders main/worktree/payments 동일 digest | CLI `TestWorkspaceStage2IdentityAndCheckoutDigest` |
| registry-only 제외, missing/identity ERROR | workspace `TestResolveTeamScope`, CLI `TestWorkspaceStage2TeamMembershipAndBaseOverride`, `TestWorkspaceStage2IdentityAndCheckoutDigest` |
| CLI → team → 기본 base 우선순위 | composition `TestTeamReplayOmittedHomeAndTeamBase`, CLI `TestWorkspaceStage2TeamMembershipAndBaseOverride` |
| no-team digest/단일 repo 출력 보존 | composition `TestStageOneDigestUnchangedWithoutTeam`, 기존 single-repo golden과 workspace 회귀 테스트 |
| 방향 × 변경 × 4칸, overlap, required_added, 중간 칸 판정 제외 | contracts `TestCheckLinkDirectionalMatrix`, `TestCheckLinkConsumerMatrix`, `TestCheckLinkOverlapAndRequiredAddition` |
| 분석 불가 문서·pointer·unanalyzed·손상 선언 | contracts `TestCheckLinkIncompleteInputs`, `TestCheckLinkMalformedBaselineCannotPass`, CLI `TestWorkspaceStage2MalformedConsumes` |
| 연결·필드 삭제/축소와 retirement | contracts `TestCheckLinkObligationRetirement`, `TestCheckLinkRetirementMustCoverDroppedObligation`, composition `TestTeamCacheLinksAndObligations` |
| producer/consumer 단독 변경 FAIL, 수정 단서 | CLI `TestWorkspaceStage2ConsumerOnlyFailure`, `TestWorkspaceStage2RenderingAndMCP`, 아래 실제 데모 |
| 충돌/--only incomplete, 부분 replay, --run exit | CLI `TestWorkspaceStage2ConflictAndPartialReplay`, `TestWorkspaceStage2MalformedConsumes`, composition `TestTeamLinkOutsideScope` |
| connect 보존·갱신·into·pointer/fields 오류·home·동시성·Git 보존 | CLI `TestWorkspaceConnectPreservesAndUpdates`, `TestWorkspaceConnectIntoAndRejectsInvalidInputs`, `TestWorkspaceConnectConcurrent` |
| connect 경로 탈출/제외 repo/긴 ID/기본 ID 충돌 | CLI `TestWorkspaceConnectRefusesSymlinkState`, `TestWorkspaceConnectIgnoresUnavailableRegistryOnlyRepo`, `TestWorkspaceConnectBoundsDefaultIDForLongRepoIDs`, `TestWorkspaceConnectDefaultIDSeparatesProducerDocuments` |
| 제안의 literal path/method 제약과 오탐 방지 | discovery MatchAcross 회귀 테스트, CLI `TestWorkspaceStage2SuggestedLinksDoNotAffectVerdict` |
| JSON 전체/텍스트·MCP 최대 3개, 실행·connect MCP 미노출 | CLI `TestCompactWorkspaceSuggestionsBounded`, `TestWorkspaceStage2SuggestedLinksDoNotAffectVerdict`, 기존 MCP 안전성 테스트 |
| 연결 pass/fail/incomplete/팀 범위 schema 적합성, help 계층 | CLI stage2 `checkStage2Schema`와 `TestHelpShowsOnlyImplementedWorkspaceSurface` |

## 실제 검증 명령

최종 코드에서 전체 일반 테스트와 전체 race 테스트가 종료 코드 0으로 통과했다. 최초 제한 실행의 실패를 성공으로 집계하지 않는다.

- `go build ./...`, `go vet ./...`, `go build -o /tmp/radar ./cmd/radar`: 통과.
- 소유 패키지와 stage2/기존 workspace/golden/MCP/onboarding 집중 테스트: 통과.
- `GOCACHE=/tmp/radar-go-build go test -count=1 ./...`: 전체 패키지 통과, exit 0. CLI 패키지 112.520s.
- `GOCACHE=/tmp/radar-go-build go test -count=1 -race ./...`: 전체 패키지 통과, exit 0. CLI 패키지 110.745s.
- `go test ./... -timeout 180s`와 race 동일 제한의 초기 동시 실행: 전체 CLI 패키지 3분 제한으로 중단; 나머지 패키지 통과. 최종 전체 검증은 결과 캐시를 끄고 기본 제한으로 실행했다. 빌드 캐시는 허용된 임시 경로에 둔다.
- 컴파일한 전체 CLI 일반/race 테스트 바이너리도 올바른 `internal/cli` CWD에서 각각 PASS, exit 0. 직접 실행의 첫 시도는 repo 루트 CWD여서 상대 fixture 파일을 찾지 못했으며, 이를 수정해 전체 재실행했다.
- `bash scripts/smoke.sh /tmp/radar`: 통과. 개별 두 브랜치 PASS, 결합 FAIL은 기대 결과다.
- `make demo-all`: 통과. 최초 sandbox 실행은 로컬 socket PermissionError였고 socket 허용 환경에서 전체 재실행했다. 시간 측정에서 wall clock 역행한 행은 unavailable로 남아 있으므로 성능 주장에 사용하지 않는다.
- `bash scripts/demo_workspace_links.sh /tmp/radar`: 통과. 아래 출력 참조.
- `git diff --check`: 통과.

repo 코드 실행은 제품의 명시적 `--run` 경계에 그대로 한정된다. 이 검증은 로컬 Linux 개발 환경의 구현 근거이며 cross-repo 런타임 호환성이나 hosted CI 결과를 보증하지 않는다. 기존 submodule 전용 fixture 부재와 repo별 실행 시간 예산은 1단계 부채로 유지한다.

## 3단계 전에 정할 질문

1. 전체 실행 시간 예산: workspace 전체 한도 **추천** / repo별 한도 유지.
2. 시나리오 실행 환경: 명시적 argv·CWD·env 이름을 가진 선언 **추천** / repo별 스크립트 이름만 지정.
3. 시나리오 누락의 기본 정책: NOT VERIFIED로 표시하고 필수 요구일 때 차단 **추천** / 항상 필수.
4. rollout 중간 칸 실패 정책: 기본 경고를 유지하고 명시적 정책에서만 차단 **추천** / 항상 차단.
5. 의존성 준비: 사전 준비된 환경만 사용 **추천** / 별도의 명시적 설치 opt-in 설계.

## 두 repo 실제 터미널 출력

`bash scripts/demo_workspace_links.sh /tmp/radar` 실행 결과다. 임시 fixture는 실행 후 제거된다. 절대 경로 한 개만 `<fixture>`로 치환했고, 판정·SHA·run ID·digest는 실제 관측값을 유지했다.

```text

$ radar workspace add <payments> --name shop
Created workspace "shop": orders (<fixture>/orders), payments (<fixture>/payments)
Radar recorded paths only: no remote access, no installs, no tests.

Next: radar gate

$ radar workspace connect orders:openapi.json# payments --fields total,status --direction response --id order-response --source client.py
Declared link order-response: orders:openapi.json# → payments (response)
  wrote <fixture>/orders/.radar/workspace.json
  wrote <fixture>/payments/.radar/consumes.json
  git -C <fixture>/orders add .radar/workspace.json && git -C <fixture>/orders commit -m 'Declare link order-response'
  git -C <fixture>/payments add .radar/consumes.json && git -C <fixture>/payments commit -m 'Declare link order-response'
Commit both files before gate reads the declarations.

Next: commit both declarations, then: radar gate --again

$ git add and commit declarations in each repository

$ radar gate orders:agent/api  # static PASS
Radar gate: PASS (static) — workspace "shop" · 2 repos · 1 branch · 1/1 cross-repo links checked

  orders    main@c33d6cb41643 + agent/api@6b952e6225c1   (named)
  payments  main@25f1381c7788                            (base only)

  ✓ orders    1 branch combined · no breaking contract change
  ✓ payments  base only · no breaking contract change
  ✓ link order-response  orders → payments

  · run 20261010T003910.352Z-d0625a · digest sha256:ba67b19af853d966

Next: radar gate --again --run

$ commit producer field removal on agent/api

$ radar gate --again  # expected FAIL, exit 1
Radar gate: FAIL — workspace "shop" · 2 repos · 1 branch · 1/1 cross-repo links checked

  orders    main@c33d6cb41643 + agent/api@db646baef75b   (named)
  payments  main@25f1381c7788                            (base only)

  ✓ orders    1 branch combined · no breaking contract change
  ✓ payments  base only · no breaking contract change
  ✗ link order-response  orders → payments: declared consumer field total is missing; consumer fields explicitly declared (runtime usage is not proven) (candidate+candidate)
  ! link order-response  orders → payments: producer first fails — declared consumer field total is missing; consumer fields explicitly declared (runtime usage is not proven) (candidate producer + base consumer; excluded from verdict)
  · again: same branches as run 20261010T003910.352Z-d0625a; orders:agent/api has new commits

Repair leads (where to look, not proof of cause)
  orders:agent/api  openapi.json  declared consumer field total is missing; consumer fields explicitly declared (runtime usage is not proven)
  payments:main  .radar/consumes.json  declares consumed fields

  · run 20261010T003910.911Z-47d1e3 · digest sha256:20a489a3e77770e6

Next: repair and commit on the branches above, then: radar gate --again

$ restore total and commit on agent/api

$ radar gate --again  # repaired static PASS
Radar gate: PASS (static) — workspace "shop" · 2 repos · 1 branch · 1/1 cross-repo links checked

  orders    main@c33d6cb41643 + agent/api@476ed5d344e6   (named)
  payments  main@25f1381c7788                            (base only)

  ✓ orders    1 branch combined · no breaking contract change
  ✓ payments  base only · no breaking contract change
  ✓ link order-response  orders → payments
  · again: same branches as run 20261010T003910.911Z-47d1e3; orders:agent/api has new commits

  · run 20261010T003911.417Z-379129 · digest sha256:1fb1bf768f193b98

Next: radar gate --again --run

$ radar gate --replay last  # reproduce pinned team and commits
Radar gate: PASS (static) — workspace "shop" · 2 repos · 1 branch · 1/1 cross-repo links checked · replay of 20261010T003911.417Z-379129

  orders    main@c33d6cb41643 + agent/api@476ed5d344e6   (named)
  payments  main@25f1381c7788                            (base only)

  ✓ orders    1 branch combined · no breaking contract change
  ✓ payments  base only · no breaking contract change
  ✓ link order-response  orders → payments

  · replayed run 20261010T003911.417Z-379129 · digest sha256:1fb1bf768f193b98

Next: radar gate --again --run
```
