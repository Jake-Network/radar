---
created: 2026-10-14
checked: 2026-10-14
---

# Radar 로드맵: 포지셔닝과 issue 목록

대상 코드는 `Jake-Network/radar@d13bf0e`이다.

## 구현 추적

현재 구현과 실행한 검증, 지원 범위, 외부 관측이 필요한 acceptance는
[`docs/ROADMAP_IMPLEMENTATION.md`](docs/ROADMAP_IMPLEMENTATION.md)에 구분해 기록한다.
아래 issue 본문은 목표를 설명하며, 본문이 있다는 이유만으로 완료된 기능이나 측정 결과를 뜻하지 않는다.
#4의 네트워크는 Actions wrapper에만 둔다. #0의 재생 base는 두 부모의 merge-base다.

## 포지셔닝

coding agent 사용자가 막히는 지점은 빈도 순으로 다음과 같다.

1. **검증과 리뷰 대역폭**: agent가 "done"이라고 한 결과를 사람이 하나씩 확인한다. agent 수가 늘수록 사람의 확인이 병목이 된다.
2. **환경과 컨텍스트**: 테스트가 돌아가는 환경, 리포 규약 이해.
3. **병렬 agent의 통합 충돌**: 각자 green인 브랜치가 합쳐서 깨진다. 텍스트 충돌은 git이 잡고, radar만 잡는 semantic break는 그보다 드물다.

현재 문구 "Catch broken merges between parallel AI coding agents"는 3번만 겨냥한다. radar의 핵심 설계인 "PASS를 과장하지 않고 확인하지 못한 것을 증거로 남긴다"는 1번에 맞고, agent를 하나만 쓰는 사용자에게도 해당한다.

- **한 문장**: agent가 "done"이라고 할 때, 실제로 무엇이 검증됐고 무엇이 검증되지 않았는지 알려준다.
- **킬러 시나리오**: 병렬 브랜치 통합 검사. 정적 symbol, migration, 계약 판정으로 merge 전에 깨짐을 잡는다.
- **차별점**: 결정적 판정(LLM 없음), 증거 모델, 사용자 브랜치를 쓰지 않는 private candidate. Claude Code나 Codex가 "worktree를 머지해 테스트 실행"을 기본 기능으로 넣어도 남는 영역이 이 셋이다.

### 성공 지표와 이정표

| 단계 | 지표 | 근거 issue |
|---|---|---|
| 검증 | 병렬 agent 사용자 10명의 첫 실행 기록, history replay 정적 검출률 | #0, #10 |
| 출시 | star 1k, 출시 주간 설치 수 | #11~#14 |
| 정착 | 매주 쓰는 팀 3곳, 오케스트레이터 통합 2개 | #4, #13 |
| 확장 | 외부 기여자 10명, star 10k | #15 |

star보다 매주 쓰는 사용자 수를 먼저 본다. 1k에서 10k로 가는 동력은 반복 사용에서 나온다.

## issue 목록

기능 issue 본문은 `.github/ISSUE_TEMPLATE/feature_request.md`의 네 절을 따르고, 제품과 출시 issue(#10~#15)는 같은 네 절을 Problem, Proposed work, Acceptance, Limitations로 쓴다.

권장 착수 순서는 다음과 같다. 출시(#14)는 첫 실행 경험(#11)과 검출률 숫자(#0)가 나온 뒤에 한 번에 한다.

1. 검증: #0, #10
2. 기본 경로 채우기: #3 → #2 → #1
3. 첫 인상: #11, #12
4. 배포: #13, #4
5. 출시: #14
6. 기여와 확장: #15, #5 → #6 → #7 → #8 → #9

| # | 제목 | 우선순위 | 선행 issue | 실행 필요 |
|---|---|---|---|---|
| 0 | History replay benchmark로 실제 merge break recall 측정 | P0 | - | 아니오 |
| 1 | Symbol 단위 semantic merge conflict 검출 | P0 | #3 | 아니오 |
| 2 | Migration 순서 충돌 detector | P0 | - | 아니오 |
| 3 | Workspace manifest 기반 monorepo import 해석 | P0 | - | 아니오 |
| 4 | 열린 PR 매트릭스 정적 gate | P0 | #1, #2 | 아니오 |
| 5 | Runner profile과 lockfile hash 기반 의존성 캐시 | P1 | - | 예 |
| 6 | Manifest와 lockfile 정합성 검사 | P1 | #3 | 아니오 |
| 7 | 생성물 drift 검사와 Protobuf, GraphQL 계약 | P1 | - | 선택 |
| 8 | 합친 트리의 uniqueness 규칙 | P1 | - | 아니오 |
| 9 | Coverage 관측으로 test impact 보강 | P2 | #5 | 예 |
| 10 | 병렬 agent 사용자 10명 첫 실행 인터뷰 | P0 | - | - |
| 11 | 설정 없는 첫 실행에서 10초 안에 판정 표시 | P0 | #1, #2, #3 | 아니오 |
| 12 | README 첫 화면과 사용자 어휘 단순화 | P0 | #0 | - |
| 13 | Agent plugin marketplace와 오케스트레이터 통합 | P1 | #11 | 아니오 |
| 14 | 출시: 데모 GIF, 검출률 숫자, 커뮤니티 동시 게시 | P1 | #0, #11, #12 | - |
| 15 | 기여 진입로: detector 단위 good first issue | P1 | #2 | - |

---

## #0 History replay benchmark로 실제 merge break recall 측정

**Labels**: `benchmark`, `P0`

### Engineering problem

`docs/BENCHMARKS.md`의 측정은 mutation fixture와 pinned upstream 프로젝트에 대한 test selection recall이다. 실제 팀에서 merge 뒤에 무엇이 깨지는지, radar가 그중 몇 건을 merge 전에 잡는지는 측정하지 않는다. 그래서 #1~#9의 우선순위를 사례 분포로 정할 근거가 없다.

### Proposed behavior and evidence source

`benchmarks/history/`에 다음을 수행하는 스크립트를 둔다.

1. 대상 리포의 base 브랜치에서 merge commit M을 찾고, M 뒤 N시간(기본 48h) 안에 들어온 fix 커밋이나 revert 커밋 F를 짝짓는다. F가 M의 두 부모 양쪽이 바꾼 파일을 건드릴 때만 후보로 둔다.
2. `merge-base(M^1, M^2)`를 base, 두 부모를 branches로 놓고 `radar gate --json`(정적)을 실행한다. `--run`은 사용자가 허가한 리포에서만 실행한다.
3. 후보마다 radar 판정과 사람이 붙인 원인 라벨(symbol, migration, lockfile, generated, route, runtime, 기타)을 기록한다.

산출물은 원인별 건수, 원인별 radar 검출 건수, 오탐 건수를 담은 표다.

### Reproducible acceptance scenario

- 내부 Python 모노레포 1개와 OSS 리포 3개(Go, TS, Java 각 1개)에서 원인별 분포표가 나온다.
- 같은 commit 목록으로 다시 실행하면 같은 표가 나온다.

### Limitations and alternatives

fix 커밋 휴리스틱은 원인을 뒤섞는다. 사람이 라벨을 붙여야 하므로 리포당 표본은 수십 건 단위로 제한한다. squash merge만 쓰는 리포는 branch tip이 남지 않으므로 PR API로 head SHA를 복원해야 한다.

---

## #1 Symbol 단위 semantic merge conflict 검출

**Labels**: `analysis`, `languages`, `P0` · **Depends on**: #3

### Engineering problem

텍스트 충돌 없이 합친 트리가 깨지는 가장 흔한 유형은 두 가지다.

- **Dangling reference**: 브랜치 A가 선언 X를 rename, 삭제, 시그니처 변경하고, 브랜치 B가 X 참조를 새로 추가한다.
- **Duplicate definition**: A와 B가 같은 namespace에 같은 이름을 서로 다른 파일로 추가한다. Go의 같은 package 안 같은 func, Java의 같은 package 안 같은 class, Rust의 같은 module 안 같은 item, C++의 같은 namespace 안 같은 정의가 해당한다.

지금은 두 유형 모두 `--run`으로 build나 test가 실패해야만 드러나고, 정적 `PASS (static)`은 두 유형에 대해 아무것도 말하지 않는다.

### Proposed behavior and evidence source

- `internal/indexer`의 import edge를 이름 단위로 확장한다. 대상은 Python `from m import X`, TS/JS named import와 re-export, Java single-type import와 같은 package 참조, Go `pkg.Ident` selector, Rust `use` 경로, C/C++ header에 선언된 함수와 타입이다.
- `integration.Analyze`에 `symbol_conflicts` 검사를 추가한다.
  - base에 있던 선언 X가 합친 트리에 없고, 어느 브랜치가 base 대비 X 참조를 새로 추가했으면 FAIL로 판정한다.
  - 합친 트리의 한 namespace에 같은 이름의 선언이 두 브랜치에서 하나씩 들어왔으면 FAIL로 판정한다.
  - 해석할 수 없는 참조(Python 속성 호출, TS dynamic import, C++ overload, macro)는 `unknown`으로 남기고 판정에 넣지 않는다.
- 결과는 증거 edge의 출처를 `inferred`로 표시하고, `fix:` 줄에 양쪽 브랜치의 파일과 줄을 적는다.
- policy 검사 이름은 `no_symbol_conflicts`로 하고, agent Stop hook preview에도 포함한다.

### Reproducible acceptance scenario

언어마다(Python, TS, JS, Go, Java, Rust, C++) fixture 3개를 둔다.

1. A가 `foo`를 `bar`로 rename하고 B가 `foo`를 새로 호출한다 → `radar gate`가 FAIL `symbol_conflicts`를 낸다.
2. A와 B가 같은 namespace에 같은 이름을 서로 다른 파일로 추가한다 → FAIL을 낸다.
3. B가 `obj.foo()`처럼 해석할 수 없는 형태로 참조한다 → FAIL 없이 `unknown`을 1건 보고한다.

### Limitations and alternatives

compiler 없이 type을 해석하지 않으므로 method 호출과 overload는 범위 밖이다. 시그니처 변경은 인자 개수 변화만 판정하고 타입 변화는 판정하지 않는다. compiler 기반 검사(`go vet`, `tsc --noEmit`)를 실행하는 대안은 #5의 runner profile 위에서 `--run`으로 제공한다.

---

## #2 Migration 순서 충돌 detector

**Labels**: `analysis`, `P0`

### Engineering problem

두 브랜치가 migration을 하나씩 추가하면 파일이 달라서 텍스트 충돌이 없지만, 합친 트리는 migration 그래프가 갈라져 배포 시점에 실패한다. 예를 들어 Alembic은 head가 둘이 되면 `alembic upgrade head`가 실패해 앱이 기동하지 않는다. import 기반 test selection은 이 상황을 검사하는 테스트를 고르지 못한다.

### Proposed behavior and evidence source

파일 파싱만으로 판정하는 내장 detector를 둔다. 실행은 하지 않는다.

| 도구 | 판정 |
|---|---|
| Alembic | `versions/*.py`의 `revision`, `down_revision`을 읽어 head가 둘 이상이면 FAIL |
| Django | `migrations/*.py`의 `dependencies`를 읽어 app별 leaf가 둘 이상이면 FAIL |
| Flyway | `V<version>__*.sql` 버전 중복이면 FAIL, base head보다 낮은 버전이 새로 들어오면 경고 |
| golang-migrate, sqlx, diesel | 버전 접두어 중복이면 FAIL |
| Prisma | `migrations/<timestamp>_*` 중 base 최신보다 이른 timestamp가 새로 들어오면 경고 |
| Liquibase | changeSet `id`와 `author` 쌍 중복이면 FAIL |

migration 디렉토리는 관례 경로로 찾고, `.radar/migrations.json`으로 경로와 도구를 지정할 수 있게 한다. `fix:` 줄에는 바꿀 값을 적는다. 예: `apps/x/alembic/versions/abc.py의 down_revision을 "def"로 바꾼다`.

policy 검사 이름은 `migration_order`로 한다.

### Reproducible acceptance scenario

도구마다 fixture를 둔다. base의 head가 R0이고 A와 B가 각각 `down_revision = R0`인 리비전을 추가하면 `radar gate`가 FAIL `migration_order`를 내고, 두 리비전 파일과 수정할 `down_revision` 값을 출력한다. B를 A 위로 rebase한 경우에는 PASS가 나온다.

### Limitations and alternatives

Alembic의 `down_revision`이 상수가 아닌 표현식이면 `unknown`으로 둔다. 같은 테이블을 서로 다른 migration에서 바꾸는 내용 충돌은 판정하지 않는다.

---

## #3 Workspace manifest 기반 monorepo import 해석

**Labels**: `indexer`, `languages`, `P0`

### Engineering problem

`internal/indexer/resolve.go`의 Python 해석은 importer 디렉토리, repo 루트, `src/`, importer의 조상 중 project marker가 있는 디렉토리만 본다. uv workspace에서 `apps/a`가 `libs/b/src/b`를 import하면 edge가 생기지 않아서, `libs/b`를 바꾼 브랜치는 "Uncovered changes"가 되고 기본 policy에서 NOT VERIFIED가 된다. `docs/CAPABILITIES.md`도 Cargo workspace metadata, tsconfig project references, PYTHONPATH를 미지원으로 적고 있다.

### Proposed behavior and evidence source

repo 안의 workspace manifest를 읽어 "패키지 이름 → 소스 루트" 표를 만들고, import 해석의 마지막 단계에서 이 표를 조회한다.

| 생태계 | 읽는 manifest |
|---|---|
| Python | 루트 `pyproject.toml`의 `[tool.uv.workspace] members`, 멤버의 `[project] name`과 `src/` 또는 패키지 디렉토리, Poetry `packages` |
| JS/TS | `package.json` `workspaces`, `pnpm-workspace.yaml`, tsconfig `references` |
| Rust | `Cargo.toml` `[workspace] members`, 멤버의 `[package] name`과 `[lib] path` |
| Go | `go.work`의 `use` |
| Java | Maven `<modules>`, Gradle `settings.gradle(.kts)`의 `include` |

해석한 edge는 `workspace_member`로 표시해서 디렉토리 관례로 해석한 edge와 구분한다.

### Reproducible acceptance scenario

생태계마다 멤버 둘(`app`, `lib`) fixture를 둔다. 브랜치가 `lib`의 파일만 바꾸면 `radar affected`가 `app`의 테스트를 고르고, `radar gate --run`의 test selection이 `complete`가 된다.

### Limitations and alternatives

glob이나 변수로 계산하는 멤버 목록은 리터럴 glob까지만 펼친다. Gradle script의 동적 `include`는 범위 밖이다.

---

## #4 열린 PR 매트릭스 정적 gate

**Labels**: `ci`, `workflow`, `P0` · **Depends on**: #1, #2

### Engineering problem

radar는 로컬 worktree 브랜치를 자동으로 고른다. 그런데 사람 여러 명이 일하는 리포에서 병렬성의 대부분은 열린 PR들에서 나온다. 각 PR은 base 위에서 CI를 통과하지만, PR끼리의 조합은 아무도 검사하지 않는다.

### Proposed behavior and evidence source

- `integrations/github-actions/pr_matrix.py`가 GitHub API로 base가 같은 열린 PR의 head SHA를 받아, wrapper에서 명시적으로 fetch한 뒤 `radar gate --json`으로 각 PR 쌍을 정적으로 검사한다. Go CLI는 네트워크·fetch를 하지 않는다.
- stacked PR은 ancestry로 체인을 묶어 체인의 tip 하나만 조합에 넣는다.
- 쌍마다 실패한 검사만 모아 보고한다. JSON 결과에는 쌍 목록, 실패 검사, 재현 명령이 들어간다.
- `integrations/github-actions`에 PR이 갱신될 때 그 PR과 나머지 열린 PR의 쌍만 검사하고, 실패한 쌍을 양쪽 PR에 코멘트하는 workflow 예제를 추가한다. 코멘트는 갱신할 때 같은 코멘트를 수정한다.

### Reproducible acceptance scenario

fixture 리포에 열린 PR 3개를 만든다. #1과 #2가 #1의 fixture 1번 유형으로 충돌하고, #3은 #2 위에 쌓은 stacked PR이다. Actions wrapper의 JSON은 (#1, #3 체인) 쌍 하나만 FAIL로 보고하고, #2와 #3을 따로 짝짓지 않는다.

### Limitations and alternatives

정적 검사만 한다. PR이 N개면 N(N-1)/2쌍을 merge하므로 N이 50을 넘으면 변경 파일이 겹치는 쌍만 검사하는 prefilter를 기본으로 둔다. merge queue를 쓰는 리포에서는 queue가 순차 검사를 대신하므로 이 기능의 가치가 낮다.

---

## #5 Runner profile과 lockfile hash 기반 의존성 캐시

**Labels**: `execution`, `P1`

### Engineering problem

`--run`은 committed 파일만 복사한 private copy에서 `python3 -m pytest`, `npx jest` 같은 기본 runner를 실행한다. 의존성이 없어서 runner가 시작되지 못하면 NOT VERIFIED가 되는데, 실제 프로젝트에서 이 경우가 가장 흔하다. plan의 `test_run` + `setup`으로 우회할 수 있지만, 모든 테스트 명령을 plan에 적어야 한다.

### Proposed behavior and evidence source

- `.radar/runners.json`에 package root별로 다음을 선언한다.
  - runner prefix: `uv run --frozen`, `pnpm exec`, `./gradlew`, `cargo`
  - setup 명령: `uv sync --frozen`, `pnpm install --frozen-lockfile`
  - 제외 선택자: `-m "not integration"`, `--exclude-tag slow`
- setup 산출물(`.venv`, `node_modules`, `target/`)은 radar state 디렉토리에 lockfile 내용 hash를 key로 저장하고, 같은 hash면 다시 쓴다.
- "의존성을 자동 설치하지 않는다"는 원칙은 유지한다. setup은 사용자가 선언한 명령만 실행하고, 실행 허가는 `--run`과 같은 경로로 받는다.

### Reproducible acceptance scenario

uv 모노레포 fixture에서 runner profile을 선언하고 `radar gate --run`을 두 번 실행한다. 첫 실행은 setup을 한 번 수행하고 PASS를 낸다. lockfile이 같은 두 번째 실행은 setup 없이 PASS를 낸다. 브랜치가 lockfile을 바꾸면 setup을 다시 수행한다.

### Limitations and alternatives

DB 같은 외부 서비스는 띄우지 않는다. 서비스가 필요한 테스트는 제외 선택자로 뺀다. 캐시 디렉토리 크기 상한과 LRU 정리가 필요하다.

---

## #6 Manifest와 lockfile 정합성 검사

**Labels**: `analysis`, `dependencies`, `P1` · **Depends on**: #3

### Engineering problem

A가 manifest에 의존성을 추가하고 B가 다른 이유로 lockfile을 재생성하면, merge 결과가 텍스트 충돌 없이 manifest와 lockfile이 어긋난 상태가 될 수 있다. frozen install이 실패하는 시점은 CI나 배포 단계다.

### Proposed behavior and evidence source

합친 트리에서 manifest에 선언된 직접 의존성이 lockfile에 있는지를 오프라인으로 대조한다.

| 쌍 | 대조 |
|---|---|
| `pyproject.toml` ↔ `uv.lock` | 멤버 package의 `requires-dist`와 manifest `dependencies` |
| `package.json` ↔ `package-lock.json`, `pnpm-lock.yaml` | lockfile 루트 importer의 dependencies와 manifest |
| `Cargo.toml` ↔ `Cargo.lock` | manifest 의존성 이름이 lockfile에 존재하는지 |
| `go.mod` ↔ `go.sum` | `require` 모듈 버전의 `go.sum` 항목 존재 |

두 브랜치가 같은 패키지를 서로 다른 major 버전으로 올린 경우는 경고로 보고한다. policy 검사 이름은 `lockfile_consistency`로 한다.

### Reproducible acceptance scenario

생태계마다 A가 manifest에 `left-pad`를 추가하고 lockfile을 갱신하지 않은 채 B가 lockfile을 재생성하는 fixture를 둔다. `radar gate`가 FAIL `lockfile_consistency`와 재생성 명령을 출력한다.

### Limitations and alternatives

버전 범위 해석과 의존성 해결은 하지 않는다. 존재와 이름 수준만 대조한다.

---

## #7 생성물 drift 검사와 Protobuf, GraphQL 계약

**Labels**: `contracts`, `P1`

### Engineering problem

- 생성 코드(OpenAPI client, protobuf stub, sqlc, mock)는 원본이 바뀌면 다시 생성해야 한다. A가 원본을 바꾸고 B가 이전 원본으로 생성물을 갱신하면 합친 트리의 생성물이 원본과 어긋난다.
- 계약 검사는 OpenAPI와 JSON Schema만 지원한다. gRPC를 쓰는 Go, Java, Rust, C++ 서비스의 주 계약은 Protobuf이고, GraphQL 서비스는 SDL이다.
- FE가 실행 중인 서버의 `/openapi.json`에서 client를 생성하는 구조에서는 committed spec이 없어서 계약 검사를 쓸 수 없다.

### Proposed behavior and evidence source

- `.radar/generated.json`에 `{ "sources": [glob], "outputs": [glob], "command": [...] }`를 선언한다. 정적 검사는 합친 트리에서 base 대비 sources는 바뀌었는데 outputs를 바꾼 커밋이 sources를 바꾼 커밋보다 앞서면 경고를 낸다. `--run`에서는 command를 실행해 outputs diff가 비어 있는지 검사한다.
- 계약 형식에 Protobuf를 추가한다. field number 재사용, 타입 변경, `reserved` 위반, 필수 field 의미 변경을 breaking으로 판정한다. GraphQL SDL도 추가한다. field 삭제, nullable에서 non-null로의 인자 변경, enum 값 삭제를 판정한다.
- 선택 기능으로, 계약 binding에 `generate` 명령을 지정하면 `--run`에서 브랜치별로 spec을 생성해 비교한다.

### Reproducible acceptance scenario

- `.proto` fixture에서 A가 field 3을 삭제하고 B가 다른 이름으로 field 3을 추가하면 FAIL을 낸다.
- generated fixture에서 A가 OpenAPI 원본을 바꾸고 생성물을 그대로 두면 경고를 낸다.

### Limitations and alternatives

`buf breaking`과 GraphQL Inspector가 같은 판정을 이미 한다. 이 기능의 가치는 이 판정을 branch 조합과 workspace link 위에서 한 판정으로 묶는 데 있다. 외부 도구를 policy 검사로 호출하는 adapter가 더 싸면 그쪽을 택한다.

---

## #8 합친 트리의 uniqueness 규칙

**Labels**: `analysis`, `P1`

### Engineering problem

route (method, path), error code, task 이름, i18n key, feature flag 이름처럼 리포 전체에서 유일해야 하는 key를 두 브랜치가 각자 추가하면, 텍스트 충돌 없이 런타임에 덮어쓰기나 기동 실패가 생긴다.

### Proposed behavior and evidence source

- 내장 규칙: `internal/discovery`가 찾는 endpoint (method, path)를 합친 트리에서 중복 검사한다. 대상은 FastAPI, APIRouter이고, 같은 방식으로 Express, Spring `@RequestMapping`, Gin, axum route를 discovery에 추가한다.
- 사용자 규칙: `.radar/unique.json`에 `{ "name": ..., "files": [glob], "pattern": regex | "json_pointer": ... }`로 key 추출 규칙을 선언한다. 합친 트리에서 같은 key가 두 위치에 있으면 FAIL로 판정한다.
- policy 검사 이름은 `unique_keys`로 한다.

### Reproducible acceptance scenario

A와 B가 각각 다른 파일에 `GET /items`를 추가하면 FAIL을 낸다. 사용자 규칙으로 `ERROR_CODE = "E\d+"`를 선언하고 A와 B가 같은 `E1001`을 추가하면 FAIL을 낸다.

### Limitations and alternatives

prefix를 변수로 조합하는 route는 `unknown`으로 둔다. regex 규칙은 주석 안의 key도 잡을 수 있으므로 사용자에게 범위를 좁히게 한다.

---

## #9 Coverage 관측으로 test impact 보강

**Labels**: `testselection`, `P2` · **Depends on**: #5

### Engineering problem

import 기반 선택은 런타임 읽기(설정 파일, fixture 데이터, 환경 변수, reflection)를 보지 못한다. 그래서 import edge가 없는 변경은 "Uncovered changes"가 되거나, 관련 테스트가 선택되지 않는다.

### Proposed behavior and evidence source

- `--run --observe-coverage`일 때 framework별 coverage를 함께 수집한다. 대상은 coverage.py, `go test -coverprofile`, JaCoCo, llvm-cov, c8이다.
- 테스트 → 실행된 파일 매핑을 commit SHA와 함께 SQLite에 저장한다.
- test selection은 정적 import edge와 관측된 edge의 합집합을 쓰고, 관측 edge는 `observed`로 표시한다. 관측 시점의 commit과 현재 base 사이에 바뀐 파일의 관측 edge는 `stale`로 표시하고 근거로 쓰지 않는다.

### Reproducible acceptance scenario

테스트가 `config/limits.json`을 런타임에 읽는 fixture를 둔다. 관측 전에는 `limits.json` 변경이 uncovered로 보고된다. `--observe-coverage`로 한 번 실행한 뒤에는 같은 변경이 그 테스트를 선택한다.

### Limitations and alternatives

coverage 수집은 실행 시간을 늘린다. 비 코드 파일 읽기는 coverage 도구가 기록하지 않으므로 파일 open을 추적하는 별도 수단(Python audit hook, `LD_PRELOAD`)이 필요하고, 이 수단은 플랫폼별로 따로 구현해야 한다.

---

## #10 병렬 agent 사용자 10명 첫 실행 인터뷰

**Labels**: `research`, `P0`

### Problem

병렬 agent 통합 충돌이 얼마나 자주 일어나는지, radar의 첫 실행 결과를 사용자가 어떻게 받아들이는지에 대한 관측이 없다. 기능 우선순위와 포지셔닝 문장이 가설에 머문다.

### Proposed work

- Conductor, cmux, claude-squad, Crystal 사용자와 `git worktree`로 agent를 2개 이상 돌리는 사용자 10명을 모집한다.
- 각자의 실제 리포에서 설치부터 첫 `radar gate`까지 화면 공유로 관찰한다. 개입은 하지 않는다.
- 기록 항목: 설치 시간, 첫 판정까지 걸린 시간, 첫 출력에서 `?`와 `·`의 비율, 사용자가 처음 던진 질문, 다시 쓰겠다는 의사와 그 이유, 지난 한 달 동안 겪은 merge 뒤 깨짐 사례.

### Acceptance

10명의 기록이 한 표로 정리되고, 각 행에 "막힌 지점"이 한 줄씩 있다. 막힌 지점 상위 3개가 #11과 #12의 범위를 정한다.

### Limitations

표본이 작고 지인 위주로 편향된다. #0의 정량 결과와 함께 판단한다.

---

## #11 설정 없는 첫 실행에서 10초 안에 판정 표시

**Labels**: `ux`, `P0` · **Depends on**: #1, #2, #3

### Problem

환경 없이 돌아가는 기본 검사는 textual merge와 선언된 계약뿐이다. 계약을 선언하지 않은 실제 리포, 특히 Python 모노레포에서 처음 돌리면 출력 대부분이 `?`(NOT VERIFIED)와 `·`(검사 안 함)이다. 처음 쓰는 사용자는 이 결과를 "아무것도 안 해 준다"로 읽는다.

### Proposed work

- 인자 없는 `radar`(또는 `radar gate`)의 기본 검사에 #1 symbol 충돌, #2 migration 순서, #3 workspace 해석을 넣는다. 셋 다 리포 코드를 실행하지 않는다.
- 첫 출력을 "확인한 것 N개 ✓/✗"와 "확인하지 못한 것 M개와 한 줄 이유"의 두 묶음으로 나누고, 확인한 것을 먼저 보여 준다.
- worktree 브랜치가 없으면 현재 브랜치와 base를 비교하는 단일 브랜치 모드로 자동 전환해서, agent를 하나만 쓰는 사용자도 첫 실행에서 결과를 얻게 한다.
- 출력 끝의 `Next:`는 그 리포에서 확인 범위를 가장 많이 늘리는 설정 한 가지를 제안한다. 예: `radar setup runners`.

### Acceptance

- #0에서 쓴 OSS 리포와 Qurify 규모의 Python 모노레포에서, 설정 없는 첫 실행이 10초 안에 끝나고 "확인한 것"이 1개 이상 나온다.
- #10 참가자 중 7명 이상이 첫 출력만 보고 무엇을 확인했는지 설명할 수 있다.

### Limitations

정적 검사가 늘어나면 오탐도 늘 수 있다. 오탐이 1건이라도 나오면 신뢰를 잃으므로, 해석할 수 없는 경우는 FAIL이 아니라 `unknown`으로 둔다는 원칙을 지킨다.

---

## #12 README 첫 화면과 사용자 어휘 단순화

**Labels**: `docs`, `ux`, `P0` · **Depends on**: #0

### Problem

README 첫 화면에 candidate, evidence, policy, attribution, obligation 같은 내부 용어가 나오고, 명령도 gate, watch, plan, workspace, discover, contracts로 넓게 펼쳐져 있다. 처음 보는 사람이 한 문장으로 무엇을 하는 도구인지 말할 수 없다.

### Proposed work

- 첫 화면: 포지셔닝 한 문장, 30초 GIF, 설치 한 줄, `radar` 한 명령과 그 출력 하나, #0의 검출률 숫자 한 줄.
- plan, workspace, policy, attribution은 "Advanced" 절과 `docs/`로 옮긴다.
- 사용자에게 보이는 출력의 어휘를 바꾼다. 예: `NOT VERIFIED` → `couldn't check: <이유>`, `candidate` → `combined branches`, `attribution lead` → `likely cause`. JSON 필드 이름은 호환을 위해 그대로 둔다.
- `Try it in one minute` 스크립트는 그대로 두되, GIF 아래로 내린다.

### Acceptance

#10 참가자 중 처음 보는 사람 5명이 README 첫 화면만 읽고 radar가 무엇을 하는지 한 문장으로 말할 수 있다. 첫 화면은 스크롤 한 번 안에 끝난다.

### Limitations

출력 어휘를 바꾸면 기존 스크린샷과 문서, agent skill 본문(`internal/onboarding/assets`)을 함께 갱신해야 한다.

---

## #13 Agent plugin marketplace와 오케스트레이터 통합

**Labels**: `integrations`, `distribution`, `P1` · **Depends on**: #11

### Problem

star와 설치는 대부분 사용자가 이미 모여 있는 채널에서 나온다. 지금 radar의 설치 경로는 curl 스크립트와 `radar setup`뿐이다.

### Proposed work

- Claude Code plugin marketplace와 Codex plugin 목록에 원클릭 설치 패키지를 올린다. Stop hook, skill, MCP server를 함께 담는다.
- Homebrew tap과 `go install` 경로를 README 설치 절의 첫 줄로 올린다.
- 병렬 agent 오케스트레이터(Conductor, cmux, claude-squad, Crystal, vibe-kanban)에 "merge 전 검사" 통합 PR이나 제안 issue를 보낸다. 통합 지점은 `radar gate --json`과 MCP `radar_gate` 하나로 고정하고, 판정 JSON 스키마(`schemas/`)를 안정 버전으로 선언한다.

### Acceptance

- marketplace 두 곳에 등록되어 있다.
- 오케스트레이터 2개 이상이 radar 통합을 머지하거나 문서에 올린다.

### Limitations

오케스트레이터 쪽 머지 여부는 radar가 정할 수 없다. 거절되면 radar 리포 안에 각 도구용 설정 예제(`integrations/`)를 두는 것으로 대신한다.

---

## #14 출시: 데모 GIF, 검출률 숫자, 커뮤니티 동시 게시

**Labels**: `launch`, `P1` · **Depends on**: #0, #11, #12

### Problem

첫 출시가 묻히면 두 번째 출시의 주목도는 크게 떨어진다. 출시 재료(숫자, 시연, 쉬운 첫 실행)가 갖춰지기 전에 게시하면 기회를 소모한다.

### Proposed work

- 30초 GIF를 만든다. 두 agent가 각자 green, 사람이 merge 직전에 `radar`, FAIL과 원인 줄, 고친 뒤 PASS 순서다.
- 제목은 #0의 숫자로 쓴다. 예: "N개 OSS 리포에서 merge 직후 고쳐진 깨짐 중 X%를 merge 전에 정적으로 잡았다".
- 같은 날 Show HN, r/ClaudeAI, r/ChatGPTCoding, X의 agent 개발자 커뮤니티에 게시한다. 게시 뒤 48시간 동안 댓글과 issue에 직접 응답한다.
- 블로그 글 하나에 측정 방법과 놓친 사례까지 공개한다. 한계를 먼저 밝히는 것이 radar의 증거 모델과 같은 메시지다.

### Acceptance

출시 체크리스트(GIF, 숫자, README, 설치 3경로, issue template, 응답 담당 시간)가 모두 채워진 뒤에 게시한다. 출시 주간의 star, 설치 수, 새 issue 수를 기록한다.

### Limitations

반응은 타이밍과 운의 영향이 크다. 숫자가 작게 나오면 포지셔닝을 바꿀 근거로 쓰고 출시를 미룬다.

---

## #15 기여 진입로: detector 단위 good first issue

**Labels**: `community`, `P1` · **Depends on**: #2

### Problem

10k 규모의 리포는 외부 기여자가 생태계별 지원을 넓혀 준다. 지금은 기여 단위가 크고(언어 adapter 전체), 새 기여자가 어디부터 손대야 할지 알기 어렵다.

### Proposed work

- #2 migration detector, #8 route discovery, #1의 언어별 symbol 해석, #6의 lockfile 쌍을 도구나 언어 하나 단위의 issue로 쪼갠다. 각 issue에는 fixture 위치, 판정 규칙, 테스트 명령을 적는다.
- detector 인터페이스와 fixture 규약을 `CONTRIBUTING.md`의 "Add a detector" 절로 문서화한다.
- `good first issue`와 `help wanted` 라벨을 붙이고, 첫 PR은 48시간 안에 리뷰한다.

### Acceptance

detector 단위 issue 15개 이상이 열려 있고, 외부 기여자의 첫 PR이 3건 이상 머지된다.

### Limitations

리뷰 부담이 founder 한 명에게 몰린다. detector 인터페이스를 좁게 고정해서 리뷰가 fixture와 판정 규칙 확인에서 끝나게 한다.

---

## 범위 밖

- **LLM 기반 판정**: 분석 경로의 결정성을 유지한다. LLM은 radar JSON을 읽는 agent 쪽에 둔다.
- **브랜치 자동 수정**: radar는 사용자 ref를 쓰지 않는다. `fix:` 줄의 구체성을 높이는 것으로 대신한다.
- **외부 서비스 기동**: DB와 broker 컨테이너는 띄우지 않는다. #5의 제외 선택자로 대신한다.
- **새 최상위 명령**: 출시(#14) 전까지는 최상위 명령을 늘리지 않는다. 새 검사는 `radar gate`의 기본 검사나 flag로 넣는다.
