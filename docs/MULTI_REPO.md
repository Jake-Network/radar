# Radar 여러 repo·브랜치 검증 (workspace)

> **상태: 1단계("선택과 결합")만 구현됨.** 아래 사양에서 2단계(연결), 3단계(실행 시나리오), 4단계(CI)로 표시된 명령·옵션·설정은 아직 없다. 구현된 명령은 `radar gate`의 `repo:ref`, `--base [repo:]REF`, `--with`, `--only`, `--again`, `--replay`와 `radar workspace add/remove/show`다.
>
> 이 문서는 `docs/plans/MSA_MULTI_REPO.md`(초기 기획)를 대체한다.
>
> **진행 상황(2026-10-10):** 1단계는 PR #5(`workspace-stage1`)로 리뷰 중이다. 2단계는 아직 시작하지 않았다. 2단계의 현재 상태, 코드 지도, 확정 결정, 구현 계획, 필수 테스트는 [plans/WORKSPACE_STAGE2.md](plans/WORKSPACE_STAGE2.md)에 있다. 새 세션은 그 문서부터 읽는다.

## 1단계 구현 결정

사양을 코드에 옮기면서 내린 결정이다. 사양 본문과 다르면 이 절이 우선한다.

1. **출력 언어는 영어다.** 기존 CLI 출력, JSON, README, agent skill이 모두 영어이므로 workspace 출력도 영어로 쓴다. 아래 출력 예는 구조·기호·순서만 그대로 따르고 문구는 다음처럼 옮긴다.
   - `repo별 검사만 · repo 간 연결 확인 0개` → `per-repo checks only · 0 cross-repo links checked`
   - `workspace 일부(1/2 repo)` → `partial workspace (1/2 repos)`, `일회성 범위` → `one-off scope (--with)`
   - 출처 `자동: worktree`/`지정`/`기준만` → `auto: worktree`/`named`/`base only`
   - `! … 확인 안 함 — <이유>` → `! … not checked — <reason>`, `다음:` → `Next:`
2. **help 계층.** `radar gate --help`는 기존 플래그를 그대로 보여 주고, 새 플래그 중 `--with`, `--again`만 더한다. `--only`, `--replay`, `--base repo:REF`와 `workspace remove`는 `radar help --all`에만 나온다.
3. **쉼표 축약은 모든 위치 인자에 같은 규칙을 쓴다.** 인자 전체를 먼저 대상 하나로 해석하고, 실패하면 쉼표로 나눠 각각 해석한다. Git ref에는 `:`가 들어갈 수 없으므로 모호하지 않다. MCP `targets`도 같은 함수를 쓴다.
4. **workspace 모드의 `--policy`는 모든 repo에 같은 정책을 적용한다. `--plan`은 오류다.** plan은 repo 하나에 묶이므로 `radar gate --only <id> --plan …`처럼 한 repo로 제한하면 쓸 수 있다.
5. **미등록 repo는 바뀌지 않는다.** workspace에 등록되지 않고 `--with`도 없으면 위치 인자와 `--base` 값을 기존과 똑같이 해석한다(`x:y`를 repo로 나누지 않는다).
6. **자동 선택의 이름순 정렬은 workspace 모드에만 적용한다.** 단일 repo 모드는 기존처럼 `git worktree list` 순서다.
7. **`--again`도 선택을 바꾸는 옵션(위치 인자, `--base`, `--with`, `--only`)과 함께 쓰면 오류다.** `--again`은 직전 선택을 그대로 반복하기 때문이다.
8. **실행 기록 위치.** 등록된 workspace는 `<state>/runs/<workspace 이름>/`, 미등록 repo에서 `--with`로 만든 일회성 범위는 `<state>/runs/_with-<현재 repo ID>-<해시>/`를 쓴다. 그래서 같은 repo의 어느 worktree에서 실행해도 `--again`이 같은 기록을 찾는다. `--replay`는 새 기록을 만들지 않는다.
9. **경로 확인.** 레지스트리의 경로가 없거나 다른 repo를 가리키면 그 repo는 ERROR(exit 2)다. 단, 현재 실행 중인 checkout이 같은 common git dir이면 그 checkout을 쓴다.
10. **같은 repo에 `--base`를 두 번 주거나, `--only`가 제외한 repo를 대상·기준으로 지정하면 오류다.**
11. **자동 선택에서 모든 repo에 기준을 넘는 브랜치가 없으면** 단일 repo처럼 "no branches to combine" 오류(exit 2)를 낸다.
12. **`--run`의 시간 제한(`--timeout`)은 repo마다 따로 적용된다.** 3단계 전에 전체 예산으로 바꿀지 정한다.
13. **실행 기록을 저장하지 못해도 판정과 exit code는 바뀌지 않는다.** `! run not recorded` 줄과 JSON `record_error`로 알린다.
14. **`--with`와 `workspace add`의 상대 경로는 repo 루트 기준이다**(`--root`를 주면 그 디렉터리 기준).

## 2단계 확정 결정 (2026-10-10, 아직 구현되지 않음)

1. **팀 파일:** 레지스트리에 home repo를 기록하고, home repo의 기준 브랜치 커밋에서 `.radar/workspace.json`을 읽는다.
2. **멤버 불일치:** 범위는 팀 파일을 따른다. 레지스트리에만 있는 repo는 빼고 `· registered but not in the team file` 줄로 알린다.
3. **`workspace connect`:** `--into repo:PATH`로 쓸 worktree를 고른다. 생략하면 레지스트리 경로(main worktree)에 쓰고, 두 repo의 커밋 명령을 출력한다.
4. **후보 연결 제안:** 변경이 있는 repo 쌍에서 찾은 후보만 최대 3개 보여 주고, 각각에 확정 명령 한 줄을 붙인다. 나머지는 JSON에만 둔다.
5. **`consumes.json`:** `fields`는 v1 `contracts.json` 표기(점 경로, `[]`)를 쓰고 `FieldOverlaps`를 재사용한다. `source`가 없으면 `·` 참고 줄로만 알린다.

구현 계획과 작업 전에 확인할 작은 결정은 [plans/WORKSPACE_STAGE2.md](plans/WORKSPACE_STAGE2.md)에 있다.

---

## 2.1 목표와 대상

> 여러 repo와 브랜치에 나눠 만든 변경을 함께 검사하고, 어디를 고치고 무엇을 다시 확인할지 보여준다.

- 첫 대상: 한 개발자나 작은 팀이 2개 이상의 repo(프런트/백엔드 2개 포함)를 동시에 수정하는 경우. 여러 에이전트가 각 repo의 worktree에서 병렬 작업한다.
- Radar는 사용자 브랜치를 checkout·수정·merge·push하지 않고, 에이전트를 실행하지 않는다.
- 결과는 "선택한 변경 + 선언한 관계 + 실행한 시나리오"에 대한 근거일 뿐이며, 운영 안전 전체를 보증하지 않는다.

## 2.2 UX 최우선 원칙

이 기능의 성패는 **사용자가 "지금 무엇이 검사됐는가"를 한 번도 오해하지 않는 것**에 달려 있다. 모든 설계 선택에서 아래가 다른 고려보다 우선한다.

1. **같은 명령은 어디서 실행해도 같은 범위를 검사한다.** workspace 멤버 repo나 그 worktree 어디서 `radar gate`를 실행해도 같은 workspace를 찾는다.
2. **헤드라인이 범위를 말한다.** 검사하지 않은 것이 있으면 판정 줄 자체에 드러난다. 아래 줄만 보고 알 수 있는 구조를 금지한다.
3. **표기법은 하나다.** repo를 가리키는 모든 입력·출력·설정은 `repo:ref` 또는 `repo:path`를 쓴다. Git ref 이름에는 `:`가 들어갈 수 없으므로 모호성이 없다.
4. **규칙 수를 줄인다.** 상호 배타 옵션, 예약값, repo별 부분 대체 같은 규칙을 새로 만들지 않는다. 새 규칙이 필요해 보이면 멈추고 묻는다.
5. **모든 오류는 다음 행동 한 줄로 끝난다.** 복사해서 실행할 수 있는 명령을 준다.
6. **선택의 출처를 항상 보여준다.** 각 브랜치·기준이 자동, 지정, 설정 중 어디서 왔는지 표시한다.

## 2.3 표기법

| 형태 | 의미 | 예 |
|---|---|---|
| `repo:ref` | 그 repo의 브랜치/태그/전체 SHA | `orders:agent/api`, `payments:5f3e…` |
| `repo:path` | 그 repo 루트 기준 경로 | `payments:src/order.ts` |
| `repo:path#pointer` | 파일 안 JSON pointer | `orders:openapi.json#/components/schemas/Order` |
| `:` 없는 ref | 현재 repo의 ref (기존 단일 repo 문법) | `agent/api` |

- **repo ID 규칙:** 영문자로 시작하고, 영숫자·`-`·`_`만 허용한다.
- **파싱 규칙:** 위치 인자는 첫 `:`에서만 나눈다. `:`가 없는 인자는 기존 단일 repo 파싱(쉼표 목록 축약 포함)을 그대로 따른다. (1단계 결정 3: 쉼표 축약은 `repo:ref`에도 같은 규칙으로 적용한다.)
- **경로 값의 예외:** 파일시스템 경로를 받는 값(`--with`, `workspace add`)은 `repo:` 표기를 쓰지 않는다. Windows 드라이브 문자와 충돌하기 때문이다.

## 2.4 범위를 정하는 규칙 (위에서부터 첫 번째로 해당하는 것)

1. `--replay RUN`: 그 실행 기록에 저장된 repo와 커밋.
2. `--workspace FILE`: 명시한 팀 파일. 주로 CI에서 쓴다(2단계 이후).
3. **로컬 레지스트리:** 현재 repo의 common git dir이 등록된 workspace.
4. 해당 없음: 기존 단일 repo 동작.

위 결과에 다음 두 옵션을 적용한다.
- `--with PATH`: 이번 실행에만 repo를 더한다. 현재 repo는 항상 포함된다. 반복할 수 있다. workspace가 없으면 "현재 repo + 이 repo들"로 일회성 범위를 만든다.
- `--only REPO`: 이번 실행을 지정한 repo들로 제한한다. 값은 repo ID이고, 반복할 수 있다. 헤드라인에 `workspace 일부(1/2 repo)`를 표시한다.

## 2.5 브랜치 선택 규칙

- **위치 인자가 없으면:** 범위 안의 각 repo에서 기준에 없는 커밋을 가진 worktree 브랜치를 자동 선택한다. 기존 `gateBranches` 규칙을 repo마다 그대로 적용한다. 자동 선택은 ref 이름순으로 정렬한다.
- **위치 인자가 하나라도 있으면:** 지정한 것만 들어간다. 지정되지 않은 repo는 기준 버전으로 참여한다. 이것은 기존 단일 repo 규칙("이름을 주면 그것만")과 같은 규칙이다. `@base` 같은 예약값은 만들지 않는다.
- **결합 순서:** 인자 순서가 같은 repo 안의 결합 순서다. 충돌을 피하려고 순서를 자동으로 바꾸지 않는다.
- **detached worktree:** 자동 선택하지 않는다. `repo:<SHA>`로 지정하라고 안내한다.
- **설정 파일에는 브랜치 선택을 저장하지 않는다.** 선택은 작업마다 달라지는 것이다. 반복은 `--again`이 담당한다.

## 2.6 기준(base) 규칙

- **기본:** repo마다 기존 `DefaultBranch` 탐색(`origin/HEAD`의 브랜치, `main`, `master`, `trunk`)을 쓴다.
- **지정:** `--base REF`는 현재 repo, `--base repo:REF`는 그 repo의 기준이다. 반복할 수 있다.
- **설정:** 2단계 팀 파일의 repo별 `base`. 우선순위는 CLI → 팀 파일 → 자동 탐색이다.
- **자동 탐색 실패:** 그 repo에만 오류를 내고 `--base payments:<REF>` 예시를 준다. 추측하지 않는다.

## 2.7 반복과 재현

- `radar gate --again`: **직전 실행과 같은 선택 방식**으로 최신 커밋을 다시 검사한다.
  - 직전이 자동 선택이었으면 다시 자동 선택하고, 참여 브랜치 변화(추가·제외)를 눈에 띄게 표시한다.
  - 직전이 지정이었으면 같은 ref를 다시 해석한다.
  - `--base`, `--with`, `--only`도 보존한다. `--run`은 이번 호출 기준으로 따로 정한다.
- `radar gate --replay RUN`: 그 실행의 **커밋 그대로** 재현한다.
  - `RUN`은 run ID 또는 `last`다.
  - 선택이나 기준을 바꾸는 옵션과 함께 쓰면 오류다. `--run`과는 함께 쓸 수 있다.
  - 후보를 다시 결합해 tree가 기록과 같은지 확인한다. 객체가 없거나 tree가 다르면 "재현 불가: <이유>"로 표시한다.
- 출력의 "다음:" 줄은 항상 `radar gate --again` 또는 `radar gate --again --run`이다. 긴 재검증 명령을 만들지 않는다.

## 2.8 설정: 로컬 레지스트리와 팀 파일의 분리

| | 로컬 레지스트리 | 팀 파일 (2단계부터) |
|---|---|---|
| 위치 | `<os.UserConfigDir>/radar/workspaces.json`, `RADAR_CONFIG_DIR`로 override | 홈 repo의 `.radar/workspace.json` |
| 커밋 | 하지 않음 (기계마다 다름) | 함 |
| 내용 | workspace 이름, repo ID → 경로, common git dir | repo ID, remote 식별, 기준, 연결, 시나리오, 정책 |
| 경로 | 절대 경로 | 경로 없음. 모든 파일 참조는 `repo:path` |

- 하나의 repo(common git dir)는 하나의 workspace에만 속한다. 원칙 2.2-1을 지키기 위해서다.
- 레지스트리는 `workspace add/remove`만 쓴다. 원자적 rename과 lock 파일을 사용한다. `gate`는 레지스트리를 읽기만 한다. 여러 에이전트가 동시에 `gate`를 실행해도 안전해야 한다.
- 팀 파일은 기존 `.radar/contracts.json`처럼 **커밋된 내용**을 읽는다. 커밋하지 않은 변경은 반영하지 않고 경고로 표시한다.
- 팀 파일이 있으면 범위는 팀 파일의 repo 목록이다. 레지스트리는 경로만 제공한다. 팀 파일에 있는데 레지스트리에 위치가 없는 repo는 `radar workspace add <경로> --id payments`로 안내한다.

## 2.9 명령 전체 (단계 표시)

```sh
radar gate [repo:ref | ref ...] [--base [repo:]REF] [--with PATH] [--only REPO] [--run] [--again] [--replay RUN]   # 1단계
radar workspace add PATH [--id ID] [--name NAME]   # 1단계. 첫 실행 시 현재 repo + PATH로 workspace 생성
radar workspace remove REPO                         # 1단계
radar workspace show                                # 1단계. 해석 결과를 보여주고 검사는 하지 않음
radar workspace connect PRODUCER CONSUMER_REPO --fields a,b [--direction request|response] [--id ID]   # 2단계
```

- `workspace add`
  - ID 기본값은 main worktree 폴더명(정규화)이다. 충돌하면 오류를 내고 `--id`를 요구한다.
  - 같은 repo의 다른 worktree 경로를 넣어도 같은 common git dir이면 같은 repo로 인식한다.
  - 이미 있는 ID에 다른 경로를 주면 경로를 갱신하고 `이전 → 새` 경로를 출력한다.
  - 원격 접근, 의존성 설치, 테스트 실행은 하지 않는다.
- `workspace show` 출력
  - 범위의 출처(레지스트리/팀 파일/`--with`), workspace 이름
  - repo별 ID, 경로, common dir, 기준과 출처, 자동 선택될 브랜치, dirty worktree
  - 경고: 경로 없음, submodule/symlink 포함, 기준 탐색 실패
  - 최근 실행 5개(run ID, 시각, 판정)
- `--plan`은 기존 plan 파일 옵션이므로 "미리보기" 의미로 쓰지 않는다.
- **help 계층:** `radar gate --help`와 `radar help`에는 `repo:ref`, `--with`, `--run`, `--again`, `workspace add/show`만 보인다. 나머지는 기존 `radar help --all` 계층에 둔다. (1단계 결정 2 참고)

## 2.10 출력 형식

**판정 체계는 두 층이다.**
- 전체 판정: 기존 4개를 그대로 쓴다. `PASS`(정적이면 `PASS (static)`), `FAIL`, `NOT VERIFIED`, `ERROR`. `BLOCKED` 같은 새 이름을 만들지 않는다.
- 줄별 표시: `✓` 통과, `✗` 실패, `!` 확인 안 함(이유 필수), `·` 참고.
  - NOT CONFIGURED와 NOT RUN을 따로 두지 않고 `! … 확인 안 함 — <이유>`로 통일한다.

**헤드라인 형식:** `Radar gate: <판정> — workspace "<이름>" · <N> repos · <M> branches · <범위 요약>`

범위 요약 규칙:
- 등록된 연결이 0개: `repo별 검사만 · repo 간 연결 확인 0개`
- `--only` 사용: `workspace 일부(<k>/<N> repo)`
- `--with` 사용: `일회성 범위`

**repo 줄:** `<id>  <base>@<sha12> + <ref>@<sha12> …  (<출처>)`. 출처는 `자동: worktree`, `지정`, `기준만` 중 하나다.

**dirty 표시:** 선택된 브랜치의 worktree만 줄로 보여준다. 문구는 `· <id>:<ref>  커밋된 내용만 포함 (미커밋 <n>개 제외)`이다. 나머지 worktree의 dirty 정보는 JSON에만 둔다.

**수정 단서:** 위치는 모두 `repo:ref` + `path[:line]`으로 표시하고, 제목에 "원인 증명이 아닌 조사 위치"를 명시한다.

1단계 출력 예(사양 원문; 실제 출력은 1단계 결정 1에 따라 영어):

```text
Radar gate: PASS (static) — workspace "shop" · 2 repos · 3 branches · repo별 검사만 · repo 간 연결 확인 0개

  orders    main@a12f3c9e01b2 + agent/api@b34e1d… + agent/rounding@c56a90…   (자동: worktree)
  payments  main@d78b22…      + agent/client@e90c4f…                          (자동: worktree)

  ✓ orders   브랜치 결합 2개 · 계약 변경 없음
  ✓ payments 브랜치 결합 1개 · 계약 변경 없음
  ! repo 간 연결: 확인 안 함 — 이 버전은 repo별 검사만 지원
  · orders:agent/rounding  커밋된 내용만 포함 (미커밋 1개 제외)

다음: radar gate --again --run
```

2단계 이후의 실패 출력 예(참고용, 1단계에서 구현하지 않음):

```text
Radar gate: FAIL — workspace "shop" · 2 repos · 3 branches

  orders    main@a12f… + agent/api@b34e… + agent/rounding@c56a…   (자동: worktree)
  payments  main@d78b… + agent/client@e90c…                       (자동: worktree)

  ✓ orders   브랜치 결합 2개
  ✓ payments 브랜치 결합 1개
  ✗ 연결 order-response  orders → payments: 응답에서 total 필드가 사라짐 (후보+후보)
  ! 시나리오 checkout: 확인 안 함 — --run 필요

수정 단서 (원인 증명이 아닌 조사 위치)
  orders:agent/api        openapi.json            total 제거
  payments:agent/client   .radar/consumes.json    total 사용 선언

다음: 위 브랜치에서 수정·커밋한 뒤  radar gate --again --run
```

**exit code:** 기존 규약을 유지한다. `0` 해당 호출의 필수 검사 통과, `1` 실패 또는 필수 근거 누락, `2` 판정을 만들 수 없는 설정·도구 오류.
- 범위 안 repo의 경로가 없으면 `2`다. 이때도 나머지 repo의 결과는 출력한다.
- 확인 안 한 범위는 exit 0이어도 헤드라인과 JSON에 남긴다.

## 2.11 결합 모델

1. repo마다 기준과 선택 ref를 전체 SHA로 고정한다. 이후 다시 해석하지 않는다. 수집 중 ref가 움직이면 다시 수집한다(최대 1회). 그래도 움직이면 오류다.
2. repo마다 기존 엔진으로 기준 위에 브랜치를 결합한다. 서로 다른 repo의 Git 객체는 섞지 않는다.
3. 변경 없는 repo는 기준 tree를 후보로 쓴다.
4. 한 repo에서 텍스트 충돌이 나면 그 repo와 종속 검사는 FAIL이다. 다른 repo의 결과는 계속 보여준다.
5. **조합 digest**는 repo ID, 기준 SHA, 순서 있는 입력 SHA, 후보 tree, 결합 알고리즘 버전, (2단계부터) 의미 있는 팀 설정 digest로 만든다. 절대 경로, 시각, 임시 디렉터리는 넣지 않는다. run ID와 digest는 별개다.
6. **실행 기록**은 `<os.UserCacheDir>/radar/runs/<workspace>/<run-id>/`에 report와 snapshot으로 저장한다(`RADAR_STATE_DIR`로 override). repo 안에는 쓰지 않는다. workspace당 최근 50개만 유지한다. `last`는 원자적으로 갱신한다. 동시 실행이 서로 덮어쓰지 않아야 한다.
7. **submodule 또는 symlink**를 포함한 repo는 `workspace add`와 `show` 단계에서 먼저 경고한다. `gate`에서는 그 repo만 ERROR로 표시하고 이렇게 안내한다: "submodule/symlink가 있는 tree는 결합할 수 없음. submodule로 묶은 repo는 각각 `radar workspace add`로 등록".

## 2.12 연결(계약) — 2단계

- 기존 `.radar/contracts.json`(v1)은 **repo 안의 연결**이다. workspace 연결은 **repo를 건너는 연결**이다. 문서·help·오류 메시지에서 이 한 문장 규칙으로 설명한다.
- 용어는 코드에 맞춰 `producer`/`consumer`로 통일한다. `provider`는 쓰지 않는다.
- **관계 선언**은 팀 파일에 둔다: `{"id":"order-response","direction":"response","producer":"orders:openapi.json#/components/schemas/Order","consumer":"payments"}`
- **소비 기대**는 소비 repo의 `.radar/consumes.json`에 둔다. 소비자 브랜치의 변경과 같이 버전이 관리되도록 하기 위해서다: `{"version":1,"consumes":[{"contract":"order-response","fields":["total","status"],"source":"src/order.ts"}]}`
- `radar workspace connect`가 두 파일을 함께 쓰고, 두 repo에 커밋하라고 안내한다. 사용자가 JSON을 손으로 고칠 필요가 없어야 한다.
- **판정 엔진**은 기존 diff 엔진(`classify`, `FieldOverlaps`, 방향별 규칙)을 재사용한다. 4칸 행렬:

  | producer | consumer | 의미 |
  |---|---|---|
  | 기준 | 기준 | 기존 선언이 유효한지 |
  | 후보 | 기준 | producer 먼저 배포 |
  | 기준 | 후보 | consumer 먼저 배포 |
  | 후보 | 후보 | 전부 적용 |

  - 판정 방법: 각 칸에서 consumer 필드가 producer 문서에 존재하는지 확인하고, producer 기준→후보 diff 중 consumer 필드와 겹치는 변경을 방향별로 분류한다.
  - 개발 판정은 `후보+후보` 칸이다. 중간 칸 실패는 `!`로 표시하되 rollout 정책이 없으면 판정에 넣지 않는다.
  - 분석할 수 없는 구조는 "확인 안 함 — <이유>"다. 연결이 선언돼 있으면 `NOT VERIFIED`다.
- 연결 삭제·축소는 기준 커밋의 선언과 비교한다. 기존 obligation 처리와 같은 방식이다.
- **후보 연결 제안:** 무설정 `gate` 출력에 "repo 사이에서 찾은 후보 연결"과 확정 명령 한 줄을 보여준다. 기존 `discovery.Compare`를 repo 간으로 확장한다. 별도 `discover` 명령은 만들지 않는다. 제안은 확정 전까지 판정에 영향을 주지 않는다.
- `services` 개념, 별도 소비자 JSON schema, enum/타입 처리 범위 선언은 2단계에 넣지 않는다.

## 2.13 실행 시나리오 — 3단계

- 팀 파일 `scenarios` 예: `{"id":"checkout","argv":["node","tests/radar-checkout.mjs"],"cwd":"payments:.","result":{"format":"junit","path":"payments:artifacts/checkout.xml"},"link":["payments:node_modules","orders:.venv"],"timeout_seconds":60}`
- 모든 경로는 `repo:path`다. 경로 기준을 따로 외울 필요가 없다.
- **`link`**: 원본 checkout의 의존성 디렉터리를 후보에 연결한다. 기존 plan `test_run`의 `link`와 같은 의미이고 같은 안전 규칙을 따른다. 의존성이 없어서 실행하지 못하면 메시지에 추가할 `link` 한 줄을 넣는다.
- 후보들을 동시에 materialize하고, `RADAR_WORKSPACE_MANIFEST` 환경 변수로 repo ID별 후보 경로와 조합 digest를 전달한다.
- 원본 checkout을 읽은 실행은 후보 근거가 아니다. 결과 파일이 없거나 테스트가 0개면 `NOT VERIFIED`다.
- timeout과 취소 시에는 이번 실행의 프로세스 그룹만 정리한다.

## 2.14 CI — 4단계

- `--workspace FILE`로 신뢰할 위치의 팀 파일을 지정한다. 모든 repo를 `repo:<SHA>`로 명시하고, 변경 없는 repo도 기준 SHA로 고정한다.
- 후보 checkout의 설정 자동 발견에 의존하지 않는다. 후보가 팀 파일의 repo나 연결을 지워도 단일 repo 모드로 떨어지지 않는다.
- 승인된 기준 조합(baseline)은 CLI 옵션이 아니라 보호된 팀 정책 안에 둔다.

## 2.15 첫 범위에서 제외

자동 clone/fetch, 홈·인접 디렉터리 탐색, Kubernetes, 자동 배포, DB 마이그레이션 판정, 서비스 메시, GUI, 이벤트/gRPC 계약, 이전 실행 증거의 재사용.
