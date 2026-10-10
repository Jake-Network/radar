# Radar — 에이전트 작업 계약

이 문서는 Radar 저장소에서 작업하는 모든 코딩 에이전트(Codex, Claude Code 등)의 공통 계약이다. `CLAUDE.md`는 이 파일을 그대로 불러온다. 현재 사용자 요청과 체크아웃된 코드, 실제 검증 결과가 기억·과거 계획·과거 계획과 git 이력의 과거 보고서보다 우선한다.

승인된 가역적 로컬 작업은 확인 질문 없이 구현과 검증까지 끝낸다. 파괴적 변경, 외부 게시, 자격 증명, 실질적인 제품 선택에서만 멈춘다. 독립적인 하위 작업은 해당 도구의 subagent로 병렬 처리할 수 있으며, 수정 범위를 파일·모듈 단위로 나누고 다른 작업자의 변경을 되돌리지 않게 한다. OMX(`.omx/`) 워크플로는 런타임 지원이 있거나 사용자가 요청할 때만 쓴다.

## Radar가 지키려는 것

Radar는 병렬 에이전트 브랜치들이 **결합했을 때** 깨지는 것을 병합 전에 찾는 로컬 Go CLI다. 제품의 가치는 근거의 신뢰성에 있다. 따라서 이 저장소에서 가장 흔한 실수는 "편의를 위해 모르는 것을 통과로 바꾸는 것"이다.

- 발견하지 못한 문제, 실행하지 못한 테스트, 지원하지 않는 분석, 아무것도 선언되지 않아 검사할 것이 없던 항목은 PASS가 아니다. `unknown`/`incomplete`/`blocked`로 남기고 이유를 보고한다.
- 근거 범주 `verified_static`, `verified_tool`, `observed_test`, `inferred`, `proposed`, `unknown`을 섞지 않는다. Tree-sitter는 구문 분석이고 import 관계는 `inferred`다. 실패 테스트의 브랜치 attribution은 "수정 단서(lead)"이지 원인 입증이 아니다.
- `gate.verdict`, 분석 `coverage`, 집계 `status`, 개별 `checks`는 서로 다른 값이다. 하나로 다른 것을 덮지 않는다.

## 명령

Go 버전은 `go.mod`(현재 1.23.0) 이상, **CGO와 C 컴파일러 필수**(Tree-sitter 문법이 바이너리에 내장됨).

```sh
make build                    # bin/radar
go test ./...                 # 전체 (make test)
make check                    # gofmt -l cmd internal 비어 있어야 함 + vet + go test -race ./...
go build -o /tmp/radar ./cmd/radar

# 단일 패키지 / 단일 테스트
go test ./internal/integration -run TestShallowCloneCombinesWithinItsHistory -v
go test ./internal/evidence -run 'TestGoBuildFailure|TestCargoBuildFailure'

# single-repo gate golden(txt/json/mcp) 재생성 — 의도한 출력 변경일 때만
RADAR_UPDATE_GOLDEN=1 go test ./internal/cli -run TestSingleRepoGateGolden

# 결합 시나리오 / 데모 (빌드된 바이너리 경로를 인수로 받음)
bash scripts/smoke.sh /tmp/radar     # 두 브랜치 각각 PASS, 결합은 FAIL — 의도된 결과
make demo-all                         # examples/*/demo.sh 전부
```

CI(`.github/workflows/ci.yml`)는 위에 더해 `integrations/github-actions/test_workflow.py`, `scripts/release-test.py`, `benchmarks/` unittest, installer·release packaging 검사, actionlint와 `scripts/workflow-test.py`, Windows installer 검사를 돌린다. 데모와 smoke는 Python 3와 Node가 필요하다.

테스트 환경: `internal/cli`의 `TestMain`이 `RADAR_CONFIG_DIR`(workspace registry)와 `RADAR_STATE_DIR`(run records)를 임시 디렉터리로 바꾸고 `FORCE_COLOR`/`CLICOLOR_FORCE`/`NO_COLOR`를 지운다. 다른 패키지 테스트나 수동 실행에서 workspace 기능을 건드릴 때는 두 변수를 직접 임시 경로로 지정해 실제 사용자 등록을 바꾸지 않는다. Git fixture는 `internal/integration/integration_test.go`의 `gitTest`/`put`, 결정적 SHA가 필요하면 `internal/cli/single_repo_golden_test.go`의 `fixedGit`을 재사용한다.

## 큰 그림: `radar gate`의 흐름

여러 파일에 걸친 핵심 경로다. 수정 전에 어느 단계의 책임인지 먼저 정한다.

1. **CLI** (`internal/cli/gate.go`, `gate_flags.go`): base(`origin/HEAD` → `main`/`master`/`trunk`)와 base 이후 커밋이 있는 worktree 브랜치를 고른다. 커밋된 변경만 입력이며 dirty worktree의 staged/unstaged/untracked는 제외 사항으로 보고한다. workspace에 속한 저장소면 `workspace_gate.go`가 저장소별로 같은 경로를 돌린다.
2. **비공개 후보** (`internal/integration/candidate.go` `BuildCandidate`): 임시 Git 저장소에 객체를 복사하고 `RunPrivate`로 hook·외부 설정을 막은 채 `merge --no-ff --no-verify`를 순서대로 적용한다. 원래 저장소의 refs·index·작업 트리는 절대 쓰지 않는다. 원본이 shallow clone이면 `.git/shallow` 경계를 후보에도 복사하고, 이력 부족 오류는 `gitrepo.ShallowHistoryError`(`internal/git/shallow.go`)로 감싸 `git fetch --unshallow` / `fetch-depth: 0`을 안내한다. **Radar는 fetch하지 않는다.**
3. **분석** (`integration.Analyze`): `textual_merge` → 명시 계약(`analyze.go` `analyzeContracts`, `.radar/contracts.json`) → 발견된 후보(경고만) → `checkpoint.Index`(공통 `indexer.Build`) → 역 import 영향 → `testselection.Recommend`.
4. **실행** (`--run`일 때만, `integration/execute.go` → `internal/evidence`): 선택된 argv를 선언된 CWD에서 timeout·출력 제한·정제된 환경으로 실행한다. 기본 명령 예산 16개, 생략된 필수 명령은 gate를 막는다.
5. **판정** (`integration/gate.go` `applyGate` → `internal/gate`): 기본 정책 `supported-integration`은 `textual_merge`, `no_breaking_contracts`, `--run`이면 `integration_execution`, suite가 있으면 `test_selection`을 요구한다. `--policy`는 요구사항을 고르는 데이터일 뿐 실행 권한을 주지 않는다.
6. **렌더링** (`gate.go` `renderGate`, `workspace_gate_render.go`, `render.go`, `style.go`/`internal/termui`): 색은 장식이고 모든 의미는 ✓ ✗ ? ! · 기호와 단어에 있다. `--json`과 MCP 출력은 절대 스타일링하지 않는다.

종료 코드: `gate`는 pass `0`, fail/blocked `1`, error `2`. `check`/`merge-check`의 정책 없는 기존 동작은 보존하며 모든 명령에 같은 의미를 강제하지 않는다.

## 실행 결과 분류 규칙 (`internal/evidence`)

`--run`의 결과를 "결합된 소스의 실패"와 "환경 문제"로 정확히 나누는 것이 리포트 신뢰도의 핵심이다. 오분류하면 무고한 브랜치를 지목하거나 실제 깨짐을 환경 탓으로 숨긴다.

- 판정 순서는 `runner.go`의 `outcome()`에 있다. 인식된 테스트 실패, 그리고 **저장소 상대 경로의 소스를 가리키는 컴파일 오류**(`buildfailure.go`: `go test -json`의 `build-output`, cargo의 `error[E…]` + `-->` 위치)는 `failed`다.
- 의존성·체크섬·toolchain 메시지가 하나라도 섞이면(`goDependency` 등) 소스 탓으로 돌리지 않는다. 절대 경로나 `..` 탈출 경로는 toolchain/module cache 쪽이므로 버린다. 위치는 `pathutil.ResolveInside`로 다시 검증한다.
- Python: `ModuleNotFoundError`는 환경(의존성 부재), `ImportError`(예: 브랜치가 지운 이름)는 결합 소스의 실패다. unittest 로더가 모든 import 실패 앞에 `ImportError: Failed to import test module`을 붙이므로 `ImportError`만으로 환경 문제로 분류하면 안 된다.
- runner 부재, 명령 부재, timeout, 인식되지 않은 출력은 환경/명령 문제다. exit `0`만으로 테스트 사례가 관측되었다고 주장하지 않는다(인식된 실행 수가 있어야 함).
- `Diagnosis.Kind`(`runner_missing`, `command_missing`, `module_missing`, `build_failed`)는 닫힌 enum이다. 종류를 추가하면 `diagnosis.go`, `schemas/integration-evidence.schema.json`의 enum, `integration/execute.go`의 `executionRemediation` 문구를 함께 바꾼다. Diagnosis는 고정 문장 + 식별자 형태로 제한된 이름이며 원시 출력은 넣지 않는다.

## 출력과 공개 형식

- single-repo gate의 text/JSON/MCP 출력과 종료 코드는 공개 계약이다(`internal/cli/testdata/single-repo/*.golden`). golden을 갱신할 때는 diff가 의도한 변화뿐인지 확인한다. 실패를 숨기려고 expectation을 바꾸지 않는다.
- 사람용 출력만 바꾸는 판단은 JSON에 새 필드를 노출하지 않는 방식으로 한다. 예: 선언된 계약도 base 의무도 없어 `no_breaking_contracts`가 공허하게 통과한 경우 text/workspace 렌더러는 ✓ 대신 `·` "none declared"로 보이고(`contractsUndeclared`), 판단 근거인 `Report.DeclaredBindings`는 `json:"-"`라 JSON은 그대로다.
- 근거·계획·evidence 형식을 바꾸면 `schemas/`, MCP 도구 설명(`internal/cli/mcp*.go`), 내장 에이전트 자산(`internal/onboarding/assets/` — `go:embed`), 배포용 사본(`integrations/claude-code/`, `integrations/codex/`), 관련 `examples/*/demo.sh`를 함께 갱신한다. 내장 자산과 `integrations/` 사본이 일치하는지 검사하는 테스트는 없으므로 직접 맞춘다.
- 새 명령은 `internal/cli/commands.go`의 명령 테이블에 고유 플래그와 사람용 렌더러를 갖춰 등록한다. MCP는 같은 핸들러로 재진입한다.

## 코드 구조 규칙

Go 모듈형 모놀리스이며 분석 코어에 데몬·외부 서비스·LLM 호출이 없다. 패키지 책임은 `docs/ARCHITECTURE.md`가 정본이다. 자주 어기는 경계만 적는다.

- `cmd/radar`는 진입점뿐(터미널이면 `cli.Interactive` 메뉴, 아니면 `cli.Run`). 분석 패키지는 `internal/cli`에 의존하지 않는다. 명령 파싱·렌더링은 CLI, 프로세스 실행은 `internal/evidence`, Git 서브프로세스는 `internal/git`, 분석 규칙은 소유 패키지에 둔다.
- 작업 트리와 커밋 인덱싱은 `indexer.Build` 하나를 공유한다(provider만 다름). 한쪽에만 탐지 규칙을 넣지 않는다. 테스트 inventory도 같은 원칙이다.
- 엔티티 ID는 저장소 상대 경로 + qualified scope(`function:svc/server.go#Server.Handle`). 절대 체크아웃 경로가 ID·근거에 들어가면 다른 clone/worktree에서 무효화된다. 저장소 identity는 root commit 기반이다.
- SQLite(`internal/storage`, modernc 순수 Go 드라이버)는 버전 마이그레이션과 스냅숏 원자 교체를 쓴다. 중단된 인덱싱이 부분 그래프를 공개하지 않게 한다. linked worktree에 자체 `.radar`가 없으면 main worktree의 상태를 쓴다(`internal/project`).
- Tree-sitter 파싱은 `ParseCtx` 대신 `ParseWithOptions` 콜백 + parser timeout을 쓴다(`docs/ARCHITECTURE.md` Design decisions: upstream 경쟁 조건).
- 작은 diff와 기존 유틸리티(`pathutil`, `jsonptr`, `gitrepo.RunPrivate` 등)를 우선한다. 새 의존성은 라이선스와 notice 수집(`scripts/release.sh`)을 확인한다.

## 계약·계획·근거의 무결성

- `.radar/contracts.json`의 명시적 binding만 실패를 만든다. `discover`가 제안한 후보는 경고/제안이며 manifest에 자동 편입하거나 덮어쓰지 않는다. 호환성은 구현된 schema 의미와 request/response 방향에 한정한다.
- base의 계약 의무는 head에서 manifest 삭제, binding·fields·consumer 축소로 사라지지 않는다(`ContractObligations`). 소비자 제거나 이유가 있는 retirement로 기록해야 한다.
- 계획 리뷰는 baseline과 plan digest에 결합되고 계획이 바뀌면 무효다. 실제 리뷰 없이 reviewer identity나 승인을 만들지 않는다.
- 근거는 저장소 identity, 정확한 commit/후보 tree, plan digest, argv/CWD, criterion에 결합된다. stale commit·plan, 변조, 다른 후보의 기록은 거부한다. 개별 브랜치 실행 기록을 결합 후보 근거로 재사용하지 않는다. 기록은 insert-only다.
- 로컬 hash와 리뷰 선언은 변조 탐지일 뿐 소유자 인증이 아니다. 문서와 출력에서 그렇게 표현하지 않는다.
- workspace 교차 저장소 link의 `cross_repo.status: passed`는 선언·지원된 정적 link에 대한 근거이지 런타임 상호운용성이 아니다.

## 실행·보안 경계

- 인덱싱·discovery·정적 check·기본 gate는 저장소 코드, 설치 hook, 스크립트, 설정 파일을 실행하지 않고 데이터로만 읽는다. 저장소 코드 실행은 `--run`/`--allow-execution` 같은 명시적 opt-in뿐이다.
- MCP에는 실행 권한 플래그, `test`, `approve`를 노출하지 않고 우회 경로도 만들지 않는다(`mcp_safety_test.go`). stdout은 JSON-RPC 전용이다. MCP는 상태와 계획 파일을 쓰므로 read-only라고 쓰지 않는다.
- 후보 디렉터리는 OS sandbox가 아니다. timeout, 출력 제한, 정제된 환경을 유지하고 자동 의존성 설치나 자동 fetch를 넣지 않는다.
- Git과 실행 인수는 argv로 전달한다. ref·경로·PR metadata를 shell에 보간하지 않는다. hook·fsmonitor·외부 설정 차단, symlink·submodule·경로 탈출·크기 제한(`gitrepo.MaxFileBytes` 등)을 약화하지 않는다.
- persisted evidence에는 원시 stdout/stderr와 비밀 값을 넣지 않는다. 출력 digest와 제한된 표시만 남기고, 환경 변수는 이름만 저장한다.
- Radar 개발 중 `go test`를 돌리는 것과 사용자 프로젝트 테스트를 실행하는 제품 경계는 별개다. 개발 편의로 실행 동의 경계를 없애지 않는다.

## Git 정책

- 시작 시 `git status --short`로 다른 작업자의 staged/unstaged/untracked 변경을 확인하고 보존한다. 무관한 정리, 포맷, stash, reset, clean을 하지 않는다. 커밋 요청 시 관련 경로만 stage한다.
- 원격 브랜치는 읽기 전용으로 취급하고 삭제·push하지 않는다. 태그 생성·push, release 게시, Homebrew tap 변경은 별도의 명시적 요청이 있을 때만 한다(`docs/RELEASING.md`).
- `.radar/state.db*`, `.radar/config.json`, `.radar/plans/`, `bin/`, `dist/`, `/radar` 바이너리, 절대 경로를 담은 registry는 커밋하지 않는다. `.radar/contracts.json`, `.radar/workspace.json`, `.radar/consumes.json`과 검토 대상 fixture는 커밋 대상이다.
- 커밋 제목은 변경의 의도를 설명하고(기존 로그처럼 사용자 관점 문장) 필요하면 `Constraint:`, `Rejected:`, `Tested:`, `Not-tested:` trailer를 단다.

## 변경 완료 기준

Go 동작 변경의 기본: 수정한 Go 파일만 `gofmt`, `go test ./...`, `go vet ./...`, `go build -o /tmp/radar ./cmd/radar`, `git diff --check`. 범위에 따라 추가한다.

| 변경 범위 | 추가 검증 |
| --- | --- |
| 파서, 저장, 동시 실행, 취소, 프로세스 수명 | 회귀 테스트 + `go test -race ./...` (또는 `make check`) |
| gate, 후보 결합, 테스트 선택, 실행 분류 | `bash scripts/smoke.sh /tmp/radar`, 관련 `examples/*/demo.sh`, 넓으면 `make demo-all` |
| 실행 결과 분류(`internal/evidence`) | 소스 실패 양성 사례와 환경 문제 음성 사례(의존성/toolchain/절대 경로) 둘 다 |
| 계약, 계획, 근거 | 정상 + malformed/unsupported/missing, stale commit/plan, 잘못된 후보 근거, 의무 제거 음성 사례 |
| CLI, JSON, MCP | 플래그, text/JSON, 종료 코드, golden, `mcp_safety_test.go` |
| CI, installer, release | 관련 Python/shell 회귀 검사와 archive·추출 실행 파일 검증 (`docs/RELEASING.md`) |
| 문서만 | 경로·명령·정책을 현재 코드와 대조 + `git diff --check` |

새 탐지 규칙은 재현 가능한 fixture와 오탐 방지 사례를 함께 넣는다. 특정 fixture 이름에 맞춘 탐지, 미지원 분석을 PASS로 만드는 테스트를 만들지 않는다. 통과한 검증을 변경 없이 반복하지 않는다.

완료 보고에는 변경된 동작, 실제로 실행한 검증과 결과, 실행하지 않은 검증, 남은 제한을 쓴다. CGO cross-build, archive 검사, 로컬 native 실행, hosted CI, 공개 다운로드는 서로 다른 검증이며 실행하지 않은 플랫폼을 검증 완료로 쓰지 않는다(Windows 바이너리는 `core.autocrlf` 미처리로 아직 배포하지 않는다).

## 참고 문서

- 기능·정책: `README.md`, `CONTRIBUTING.md`, `docs/CAPABILITIES.md`, `docs/VERIFICATION_POLICY.md`, `docs/SECURITY.md`
- 설계·사용: `docs/ARCHITECTURE.md`, `docs/USAGE.md`, `docs/TEST_SELECTION.md`, `docs/MULTI_REPO.md`
- 배포: `docs/RELEASING.md`, `.github/workflows/`
