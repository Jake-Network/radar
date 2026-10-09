# Radar 여러 repo·브랜치 변경 검증 기획

작성일·수정일: 2026년 10월 10일. 상태: 구현 제안. 아래 workspace 명령, 옵션, 설정 및 출력은 아직 구현되지 않았다. 기존 단일 repo `radar gate`와 구분한다.

## 제품 목표

> 여러 repo와 브랜치에 나눠 만든 변경을 함께 검사하고, 어디를 수정하고 무엇을 다시 확인할지 보여준다.

개발자와 코딩 에이전트가 기존 repo·worktree에서 작업하는 방식을 유지한다. Radar는 각 repo의 여러 브랜치를 임시 후보로 결합하고, 후보 repo들이 함께 동작하는지 검사한다. 사용자가 매번 workspace 모델이나 lock 파일을 이해하지 않아도 시작할 수 있어야 한다.

첫 대상은 한 개발자 또는 작은 팀이 Python·TypeScript·Go 등으로 된 두 개 이상의 repo를 함께 수정하는 경우다. 프런트엔드·백엔드처럼 두 repo만 있는 프로젝트도 포함한다. 서비스 3~20개의 MSA 팀은 후속 파일럿 대상이며, 지원 성능의 약속이 아니다. 최근 개별 repo CI가 놓친 통합 실패를 경험한 사용자를 우선 모집한다.

Radar는 변경 선택, 조합 검사, 수정 단서와 재검증을 담당한다. 실제 코드는 개발자 또는 기존 코딩 에이전트가 소유한 브랜치에서 수정한다. `gate`가 사용자 브랜치를 checkout·수정·merge·push하거나 에이전트를 새로 실행하지 않는다. 검사 결과는 선택한 변경, 선언한 관계와 실행한 시나리오에 대한 근거이며 운영 안전 전체를 보증하지 않는다.

## 사용 경험 원칙

1. **일상 명령은 `radar gate` 하나다.** 여러 repo에서도 같은 명령을 쓰고, 실행할 때만 기존 `--run`을 더한다.
2. **경로만으로 시작한다.** 설정 없이 선택한 repo의 worktree 브랜치와 조합 상태를 본다. 서비스 관계를 모르면 그 범위를 명시한다.
3. **설정은 필요할 때 추가한다.** 자주 쓰는 repo를 저장한 뒤 계약 하나, 시나리오 하나씩 붙인다. 별도 조정용 repo는 선택 사항이다.
4. **선택한 대상은 먼저 보여준다.** repo, 기준, 후보 브랜치, 제외한 dirty 파일, 실행 명령과 누락된 설정을 설명한다.
5. **실패 다음 행동을 바로 보여준다.** repo·브랜치·파일·근거·재검증 명령을 제공하고 확인된 원인과 조사 단서를 구분한다.
6. **버전 고정은 도구가 처리한다.** 평소 재실행은 최신 커밋을 검사하고, 이전 결과 재현은 저장된 snapshot을 사용한다.

## 대표 사용자 흐름

### 1. 설정 없이 두 repo 검사

`orders` repo에서 실행한다. `--repo`는 반복 가능한 경로 옵션이다.

```sh
radar gate --repo . --repo ../payments
```

대상은 명시한 두 repo다. 각 repo의 기준은 기존 기본 브랜치 탐색 규칙(`origin/HEAD`, `main`, `master`, `trunk`)으로 고르고, 기준에 없는 커밋을 가진 worktree 브랜치를 선택한다. 모든 로컬·원격 브랜치를 포함하지 않는다. 변경 브랜치가 없는 repo는 기준 버전으로 참여한다.

`orders`의 `agent/api`, `agent/rounding`과 `payments`의 `agent/client`를 선택했다면, orders의 두 브랜치를 먼저 같은 repo 안에서 결합한 뒤 두 repo 후보를 함께 검사한다. 계약 연결이 없으면 repo별 검사와 `repo 간 계약: NOT CONFIGURED`를 보여준다. 이를 전체 통합 성공으로 표현하지 않는다.

### 2. 자주 쓰는 repo 저장

```sh
radar workspace init --repo . --repo ../payments
radar gate
radar gate --run
```

`workspace init`은 현재 repo의 `.radar/workspace.json`을 생성한다. 고유한 폴더명으로 repo ID를 제안하고 경로, 기준, 브랜치 선택 규칙을 출력한다. 원격 접근, 의존성 설치, 테스트 실행은 하지 않는다. 기존 파일은 덮어쓰지 않고 수정할 항목을 안내한다. 생성 전 확인에는 `--dry-run`을 사용한다.

이후 해당 repo와 그 worktree 내부에서는 workspace 설정을 찾아 `radar gate`를 쓸 수 있다. 탐색은 현재 Git repo와 그 common checkout에 한정한다. 다른 서비스 repo에서는 설정을 명시한다. 홈이나 인접 디렉터리 전체를 탐색하지 않는다.

```sh
# payments repo에서 orders에 저장한 설정 사용
radar gate --workspace ../orders/.radar/workspace.json
```

workspace 설정도 `--repo`도 없으면 현재 단일 repo 동작을 유지한다. workspace가 있는 repo에서 일시적으로 현재 repo만 검사하려면 `radar gate --repo .`을 쓴다. 그 실행은 workspace 전체 검증이 아님을 표시한다.

### 3. 이번 작업의 브랜치만 선택

```sh
radar gate --branch orders=agent/api --branch orders=agent/rounding \
  --branch payments=agent/client
```

`--branch`는 `repo ID=ref` 형식이며 같은 repo에 반복할 수 있다. 지정한 repo의 저장된 선택 또는 자동 선택을 **대체**한다. 지정하지 않은 repo는 설정의 선택 규칙을 유지한다. 자동 선택과 명시 선택을 합산하지 않는다.

```sh
# payments는 후보 변경 없이 기준 버전으로 검사
radar gate --branch orders=agent/api --branch payments=@base

# repo마다 기준이 다를 때
radar gate --base orders=develop --base payments=main
```

`@base`는 workspace 모드의 예약값이며 해당 repo를 기준에 고정한다. 다른 후보와 함께 지정하면 오류다. ref는 브랜치·태그·전체 SHA를 허용하고 실행 시 커밋으로 고정한다. detached worktree는 자동 포함하지 않으며 SHA를 명시하도록 안내한다.

workspace 모드에서 `radar gate feature/api`처럼 repo 없는 위치 인자는 모호하므로 오류와 `--branch orders=feature/api` 예시를 제공한다. 단일 repo 모드의 기존 문법은 유지한다.

### 4. 수정하고 같은 선택으로 재검증

다음은 계약과 checkout 시나리오를 등록한 후의 제안 출력이다. 브랜치·SHA·파일명은 예시다.

```text
Radar gate: FAIL — 2 repos, 3 branches

orders    main@a12… + agent/api@b34… + agent/rounding@c56…
payments  main@d78… + agent/client@e90…

  PASS     repo 내부 브랜치 결합: 2/2
  FAIL     orders → payments: 응답의 total 필드가 없음
  NOT RUN  checkout 시나리오: --run으로 실행 필요
  제외     orders / agent/rounding: 커밋하지 않은 파일 1개

수정 단서
  orders / agent/api       api/order.py:42 — 응답 변경 위치
  payments / agent/client  src/order.ts:18 — 선언된 소비자 위치
  계약 위치                orders:openapi.json#/… → payments:contracts/order.json#
  코드 위치는 조사 단서이며 런타임 원인을 증명하지 않음

다음: 의도한 수정을 각 브랜치에 커밋한 뒤 같은 선택으로 실행
  radar gate --branch orders=agent/api --branch orders=agent/rounding \
    --branch payments=agent/client --run

선택한 버전 재현: radar gate --snapshot .radar/runs/<run-id>/snapshot.json --run
```

사용자는 표시된 브랜치에서 수정하고 커밋한다. 일반 재실행은 수정된 브랜치 SHA로 새 후보와 결과를 만든다. 과거 snapshot은 당시 버전 재현에만 사용한다. 출력은 실제 조회 가능한 위치만 표시하고 모르는 위치나 수정 방법을 만들어 내지 않는다. 재검증 명령에는 실행 당시의 workspace 경로, repo별 선택·기준 override도 보존한다.

## 최소 설정과 점진적 확장

### repo 목록만 저장하는 설정

아래는 `orders/.radar/workspace.json`의 최소 예시다. 경로는 **설정 파일이 들어 있는 디렉터리** 기준이다. 생성기가 상대 경로를 계산하므로 사용자가 직접 계산할 필요는 없다.

```json
{
  "version": 1,
  "repos": {
    "orders": { "path": ".." },
    "payments": { "path": "../../payments" }
  }
}
```

기본값은 `base: "auto"`, `branches: "worktrees"`다. 기준을 결정할 수 없으면 추측하지 않고 그 repo에만 기준을 지정하도록 안내한다. 브랜치를 고정하려면 필요한 항목만 추가한다.

```json
{
  "version": 1,
  "repos": {
    "orders": {
      "path": "..",
      "base": "main",
      "branches": ["agent/api", "agent/rounding"]
    },
    "payments": {
      "path": "../../payments",
      "base": "main",
      "branches": ["agent/client"]
    }
  }
}
```

`branches: []`는 해당 repo를 기준에 유지한다. 배열과 반복 `--branch`의 순서는 결합 순서다. 자동 worktree 선택은 ref 이름순으로 정렬하고 순서를 출력·기록한다. 텍스트 충돌을 해결하기 위해 결합 순서를 자동으로 바꾸지 않는다.

### 선택 우선순위와 경로 이동

| 항목 | 규칙 |
| --- | --- |
| 검사 대상 | `--repo` 목록 또는 workspace 중 하나. 명시적으로 함께 지정하면 오류. `--repo`는 자동 발견 설정을 대신하며 현재 repo를 암묵적으로 더하지 않음 |
| 설정 선택 | 명시한 `--workspace` → 현재 repo에서 발견한 설정 → 단일 repo 기본 동작 |
| 브랜치·기준 | repo별 CLI 옵션 → 설정 → 기본값. 알 수 없는 repo ID는 오류 |
| 다른 checkout 위치 | `--path payments=/work/payments`로 등록된 ID의 경로만 대체. 검사 범위는 유지 |
| repo 구분 | 독립 common Git directory 기준. 같은 repo의 여러 worktree를 여러 repo로 등록하면 오류 |
| ID 충돌 | `--repo orders=../orders-api`처럼 ID를 명시하도록 안내. 저장한 ID는 경로가 바뀌어도 유지 |

`--repo` 값은 경로 또는 `ID=path`다. `=`가 포함된 경로는 명시 ID 뒤에 놓고 첫 `=`에서만 분리한다. repo ID는 영문자로 시작하는 영숫자·`-`·`_`로 제한한다. 공백 경로는 셸 인용을 사용한다.

repo 경로는 위치를 찾는 데만 사용하며 조합 digest에 절대 경로를 넣지 않는다. 다른 checkout 연결 시 필요한 Git 객체와, 등록된 remote 식별 정보가 있으면 그 일치를 확인한다. remote URL만으로 repo의 진위를 인증했다고 표현하지 않는다. 자동 clone/fetch는 하지 않는다.

### 계약과 테스트는 하나씩 추가

```sh
# 기존 OpenAPI, 계약 파일, 테스트 설정에서 지원하는 후보만 조사
radar workspace discover

# 검토·수정할 별도 초안 생성. 활성 설정은 그대로 유지
radar workspace discover --out .radar/workspace.suggested.json
```

기존 `.radar/contracts.json` v1은 그대로 지원한다. discovery는 지원하는 정적 패턴과 명시한 파일만 사용한다. 발견한 관계·테스트마다 출처, 신뢰 범위, 미해결 항목을 표시한다. 사용자가 확인한 항목을 활성 설정에 반영하도록 하고, 자동으로 확정 관계나 검증 근거로 승격하지 않는다. 이 명령도 `--workspace`로 설정을 명시할 수 있다.

workspace 확장 필드는 다음 역할을 가진다. 초안 생성은 모든 repo의 설정 완성을 요구하지 않는다.

| 필드 | 최소 내용 | 추가 시점 |
| --- | --- | --- |
| `services` | 서비스 ID, repo ID, 서비스 상대 경로 | 기본 1 repo = 1 서비스로 부족할 때. 한 repo에 여러 서비스 지원 |
| `contracts` | 제공·소비 서비스, 각 스키마 파일·JSON pointer, operation, 요청/응답 방향 | API 하나의 repo 간 호환성을 검사할 때 |
| `scenarios` | ID, 명령 소유 repo, 상대 CWD, argv, 결과 형식, timeout | 실제 통합 테스트를 실행할 때 |
| `policy` | 필수 관계·시나리오·검사와 실행 예산 | 팀 CI의 병합 조건을 정할 때 |
| `rollout` | 요구한 서비스 전환 순서와 검사 조합 | 순차 배포 호환성을 확인할 때 |

예를 들어 최소 설정에 다음 두 필드를 추가하면 응답 계약 하나와 checkout 시나리오 하나를 등록한다. 아래 JSON은 전체 파일이 아니라 추가할 필드의 예시다. 기본 서비스 ID는 repo ID와 같다. schema 파일은 해당 서비스의 repo 기준, scenario CWD는 명령 소유 repo 기준, 결과 파일은 CWD 기준이다.

```json
{
  "contracts": [
    {
      "id": "order-response",
      "operation": "GET /orders/{id}",
      "direction": "response",
      "provider": {
        "service": "orders",
        "schema": "openapi.json",
        "pointer": "/components/schemas/Order"
      },
      "consumer": {
        "service": "payments",
        "schema": "contracts/order-response.json",
        "pointer": ""
      }
    }
  ],
  "scenarios": [
    {
      "id": "checkout",
      "repo": "payments",
      "cwd": ".",
      "argv": ["node", "tests/radar-checkout.mjs"],
      "result": { "format": "junit", "path": "artifacts/checkout.xml" },
      "timeout_seconds": 60
    }
  ]
}
```

`tests/radar-checkout.mjs`는 사용자의 기존 테스트를 연결하는 커밋된 adapter 예시다. 후보 manifest를 읽어 HTTP 서버·클라이언트를 실행하고 인식 가능한 JUnit 결과를 작성해야 한다. Radar가 이 파일이나 결과를 자동으로 제공한다고 가정하지 않는다. 테스트 자체를 새로 만들 필요가 있는지, 기존 명령에 얇은 adapter만 필요한지 discovery 결과에 구분해 표시한다. 결과 파일은 후보 디렉터리 안에서 실행 전 비우거나 새 경로를 사용해 과거 결과를 읽지 않으며, 필수 결과가 없거나 테스트 수가 0이면 BLOCKED다. 선언된 계약은 정적 검사, 선언된 시나리오는 실행 검사의 기본 필수 항목이다. 정책 완화는 기준 의무와 비교한다.

소비자 기대는 사용하는 필드·허용값을 독립적으로 나타내야 한다. 제공자 OpenAPI 전체를 복사한 것을 실제 소비 근거로 취급하지 않는다. 기대 스키마의 소유 repo와 출처 파일을 남기고 관련 소스 변경 때 재검토 필요를 표시한다. 변경 감지 자체가 기대 스키마의 정확성을 증명하지는 않는다.

## 여러 repo·브랜치 조합 모델

처리 순서는 `선택 확인 → 입력 ref 고정 → repo별 브랜치 결합 → repo 간 계약 검사 → 선택한 테스트 → 수정 단서`다.

1. 각 repo의 기준과 선택한 모든 ref를 전체 SHA로 고정한다. 이후 움직이는 ref를 다시 해석하지 않는다. 입력 수집 중 ref 이동이 감지되면 재수집하거나 차단한다. 여러 repo를 동시에 읽은 원자적 운영 snapshot이라고 표현하지 않는다.
2. 기존 단일 repo integration 엔진으로 기준 위에 브랜치들을 결합한다. 원본 refs와 worktree는 보존하고 후보 객체는 격리된 저장소에서 관리한다.
3. repo별 후보 tree, 결합 순서·입력 SHA를 기록한다. 변경 없는 repo는 기준 SHA/tree를 후보로 쓴다. 텍스트 충돌은 해당 후보와 종속 시나리오를 차단하되 다른 repo에서 확인한 결과를 숨기지 않는다.
4. repo별 후보를 composition으로 묶어 계약·시나리오를 검사한다. 서로 다른 repo 사이에서 Git 객체를 merge하지 않는다.
5. 모든 결과 위치에 repo ID를 포함한다. 서비스 ID와 repo ID를 분리하며 같은 파일명·브랜치명이 여러 repo에 있어도 구별한다.

Dirty 파일은 포함하지 않고 repo·worktree·경로별로 보여준다. 자동 stash나 임시 커밋을 만들지 않는다. 관련 없는 worktree는 `branches` 배열이나 `--branch`로 제외할 수 있다. 자동 선택의 참여 목록 변화는 새 실행에서 눈에 띄게 표시한다.

### 자동 snapshot과 재현

평소 `gate`가 `.radar/runs/<run-id>/`에 snapshot/report를 생성한다. 저장 위치는 workspace 설정 소유 repo, 일회성 실행은 첫 선택 repo다. generated runs는 Git 추적 대상에서 제외하도록 초기화가 안내한다. baseline/candidate lock 수작업 작성은 기본 절차에서 제거한다.

snapshot은 다음을 포함하는 버전 있는 기계 생성 형식이다.

- 전체 repo 기준 SHA/tree, 순서 있는 입력 SHA, 후보 commit/tree, 결합 알고리즘 버전.
- 경로 독립적 workspace 설정, 관계·정책·시나리오 digest, 검사 조합과 예산.
- 기준 검증 의무의 출처와 실제 실행 결과를 연결하는 식별자.

조합 digest는 Git 입력·결합 순서·후보 tree·의미 있는 설정에 기반한다. 임시 경로, 실행 시각, 임의 merge commit 메타데이터로 같은 조합의 digest가 바뀌지 않게 한다. 실행 이력의 run ID와 조합 digest는 별개다. 실제 사용한 후보 commit도 별도로 기록한다.

`radar gate --snapshot <file>`은 고정 입력과 설정으로 재현한다. 브랜치·기준·검사 설정을 덮어쓰는 옵션과 함께 쓸 수 없다. `--path`로 위치만 바꾸거나 `--run`으로 같은 조합을 실제 실행할 수 있다. 파일 존재만으로 과거 PASS를 신뢰하지 않는다. 후보 객체를 보존하거나 입력으로 재구성해 tree를 확인하고, 필요한 객체·설정·도구가 없으면 재현 불가로 표시한다. snapshot은 Git 객체와 의존성을 포함하는 완전한 실행 환경이 아니다.

## 수정 안내와 에이전트 연동

read-only workspace MCP를 초기 범위에 포함한다. 기존 `radar_gate`에 workspace와 repo별 branches/base 입력을 추가하고 CLI와 동일한 선택·판정 규칙을 쓴다. 저장된 결과 조회는 ref 이동과 freshness를 함께 반환한다.

JSON/MCP의 `repair_tasks`에는 finding ID, repo ID, 기준·입력 SHA, 관련 브랜치, 파일 위치, 실패 근거, 권장 확인 사항, 다른 repo 작업과의 선후 관계, 재검증 명령을 담는다. 소유 브랜치가 불명확하면 후보 목록과 불확실성을 전달한다. 여러 에이전트에 같은 파일 수정을 중복 배정하도록 단정하지 않는다.

제공자에게 이전 필드 유지, 소비자에게 새 필드 지원 등을 제안할 수 있다. 이는 계약 실패의 수정 후보이며 자동 패치가 아니다. 에이전트는 대상 HEAD가 전달받은 SHA와 맞는지 확인하고 자기 브랜치에서 수정한다. HEAD가 바뀌었다면 새 결과로 작업을 갱신한다.

`--run`은 기존 명시적 실행 선택을 유지한다. read-only MCP가 저장소 코드 실행을 대신 승인하지 않는다. 개발자 또는 기존 CI 실행 정책에 따라 테스트하고 결과를 다시 에이전트에 전달한다.

첫 배포에 cmux 알림 및 tmux·Orca에서 CLI를 호출하는 짧은 예제를 포함한다. 별도의 에이전트 실행 도구를 요구하지 않는다. watch는 P1이며 commit 변경 시 static gate 재실행부터 시작한다. 상태가 바뀔 때만 알림을 보내 반복 소음을 줄인다.

## 계약 검사와 배포 조합

기본 개발 흐름은 repo별 결합 결과와 최종 후보를 검사한다. 지원하는 명시적 직접 관계에는 다음 정적 행렬을 제공한다. 최종 후보 개발 판정과 배포 경로 판정은 분리한다.

| 제공 서비스 | 소비 서비스 | 의미 |
| --- | --- | --- |
| 기준 | 기준 | 기존 선언의 호환성 |
| 후보 | 기준 | 제공 서비스가 먼저 변경되는 경우 |
| 기준 | 후보 | 소비 서비스가 먼저 변경되는 경우 |
| 후보 | 후보 | 선택한 변경이 모두 적용된 경우 |

주문 `total`을 `total_cents`로 바꾸고 결제도 수정하면 최종 후보는 통과할 수 있지만 중간 조합은 깨질 수 있다. `rollout`이 없으면 중간 조합 문제를 표시하되 배포 경로는 `NOT CONFIGURED`다. 전체 배포 FAIL 또는 안전한 배포로 단정하지 않는다. `rollout`이나 보호된 정책이 요구하는 조합이 실패하면 해당 배포 검증은 FAIL이다.

요청은 소비자가 보낼 수 있는 값이 제공자가 허용하는 범위에 포함되는지, 응답은 제공자가 반환할 수 있는 값이 소비자가 처리할 수 있는 범위에 포함되는지 검사한다. 첫 범위는 필드 존재·필수 여부, 지원 타입, nullability, enum이다. 미지원 construct나 모호한 방향은 UNKNOWN이며 필수 검사에서는 BLOCKED다. 기존 전후 schema diff와 서로 다른 제공·소비 문서의 호환성 비교 코어를 구분한다.

단위·권한·과금 규칙은 정적 스키마만으로 증명하지 않는다. 스키마가 같은 금액 단위 오류는 명시한 런타임 시나리오로 검사한다. 직접 관계별 통과가 전체 업무 흐름 성공을 보장하지 않는다.

런타임은 기본적으로 최종 후보 조합을 실행한다. 후속 배포 경로 지원에서는 선언한 필수 중간 조합을 추가한다. 모든 서비스·브랜치 부분집합을 생성하지 않는다. 조합·명령·시간 예산을 넘으면 누락 목록을 남기고 필수 검사는 차단한다.

기준은 사용자가 선언한 개발 기준이다. 실제 운영 버전이라고 표현하려면 출처·관측 시점·최신성 기준이 필요하다. 구버전·신버전 인스턴스 공존, 롤백, DB 마이그레이션은 입력과 지원 범위가 없으면 미검증으로 표시한다. 순환 의존성에서 배포 순서를 자동 확정하지 않는다.

## 실제 테스트: 기존 명령 재사용부터

P0에서는 기존 통합 테스트 명령을 재사용한다. 후보 repo들을 임시 디렉터리에 materialize하고 scenario 소유 repo·CWD에서 명시 argv를 실행한다. repo ID별 후보 경로와 조합 ID를 실행별 manifest로 제공하고, 그 위치를 `RADAR_WORKSPACE_MANIFEST` 환경 변수로 전달한다.

기존 테스트가 원본 checkout을 계속 읽으면 후보 조합 검증이 아니다. adapter는 manifest의 후보 경로에서 서비스를 시작해야 한다. 경로 전달만으로 실제 사용을 증명했다고 하지 않고 시작 argv·CWD·소스 식별·readiness·인식한 테스트 결과를 관측한다. 이 조건에 연결할 수 없으면 command 결과만 기록하고 candidate-bound scenario 근거는 누락으로 남긴다.

첫 E2E는 Python 주문 HTTP 서버와 Node 결제 클라이언트를 별도 repo에서 실제 실행한다. 한 repo에 두 브랜치, 다른 repo에 한 브랜치를 둔다. 작은 fixture adapter가 manifest를 읽어 시작·readiness·테스트·정리를 담당한다. DB나 클러스터 없이, 각 repo 자체 테스트는 통과하지만 조합은 실패하고 양쪽 수정 후 성공하는 흐름을 포함한다.

실행 계층은 timeout·취소 시 이번 실행의 프로세스 그룹을 정리한다. 준비 실패·환경 오류·테스트 실패·timeout을 구분하고 기존 사용자 서비스를 종료하지 않는다. 외부 Docker·원격 자원 정리는 adapter 계약과 관측 범위에 따른다. 범용 lifecycle 설정과 Compose adapter는 파일럿 수요에 따라 P1로 확장한다.

exit code만으로 PASS를 만들지 않는다. 인식한 harness 결과·실제 실행 수, 원본 및 후보 소스 무결성, 명령·CWD·환경 식별, 시작·종료 결과를 조합·관계·시나리오·정책 digest에 연결한다. 입력이 바뀌면 이전 증거를 새 조합에 재사용하지 않는다. secret은 출력·저장하지 않는다. 임시 디렉터리는 OS sandbox가 아니며 PR CI는 기존 신뢰·실행 정책을 유지한다.

## 판정과 팀 CI

사람용 출력은 repo 내부 결합, repo 간 계약, 런타임, 배포 경로를 별도 줄로 보여준다. JSON에는 실제 검사 범위, 필요한데 없는 근거, 제외된 입력, 수정 단서를 유지한다.

| 상태 | 의미 |
| --- | --- |
| PASS (static) | 이름 붙인 지원 정적 검사 통과. 런타임 성공을 뜻하지 않음 |
| PASS scenarios | 요구된 조합의 선언한 시나리오가 관측 결과로 통과 |
| NOT CONFIGURED | 선택하지 않은 기능·관계. coverage gap으로 표시하고 PASS에 포함하지 않음 |
| NOT RUN | 선언됐지만 이번 정적 호출에서 실행하지 않은 시나리오. 실행 필수 정책에서는 BLOCKED |
| BLOCKED | 요구된 커밋·관계·schema·실행 근거가 없거나 검사 불가 |
| FAIL | 확인된 계약 위반·테스트 실패·결합 충돌 |
| ERROR | 환경·도구·설정 오류. 이미 확인된 FAIL은 함께 보존 |

기본 판정은 호출 목적에 맞춰 다음과 같이 고정한다.

- `radar gate`: repo별 결합과 기존 정적 검사, 선언된 모든 최종 후보 계약을 필수로 검사한다. 실행하지 않은 시나리오는 NOT RUN이다. 실행 근거를 요구하는 명시 정책이 없으면 그것만으로 정적 결과를 차단하지 않는다.
- `radar gate --run`: 위 검사에 repo별 기존 필수 실행·선택 근거와 선언된 모든 최종 조합 시나리오를 더한다. 여러 repo의 시나리오 근거가 없으면 workspace 통합 실행은 BLOCKED다.
- 명시한 보호 정책이 런타임 또는 중간 조합을 요구하면 정적 호출에서도 그 근거가 없는 검사는 BLOCKED다. `--run` 생략으로 의무를 해제하지 않는다.

workspace 관계가 없는 첫 실행은 repo별 정적 결과를 제공하되 제목에 `repo-local only`를 붙인다. 한 repo만 선택한 호출은 기존 단일 repo의 실행 의무를 적용하며 repo 간 시나리오를 요구하지 않는다. 기존 exit code 0/1/2 규약을 유지한다. 필수 검사 위반·누락은 1, 성공적으로 판정을 만들 수 없는 설정·도구 오류는 2, 해당 호출의 필수 검사가 모두 통과하면 0이다. 선택하지 않은 범위의 NOT CONFIGURED/NOT RUN은 exit 0이더라도 출력·JSON에 남긴다. 확인된 FAIL과 함께 환경 문제가 있으면 둘 다 보존한다. 설정 삭제만으로 보호된 의무가 사라지거나 전체 통합 PASS가 나오지 않아야 한다.

CI는 자동 worktree 선택 대신 모든 repo의 기준과 후보 SHA를 명시한다. 기존 PR 입력을 repo ID에 연결하고 변경 없는 repo도 기준 SHA로 고정한다. snapshot/report를 artifact로 남긴다. PR 재작성이나 설정 변경 후 이전 결과는 재사용하지 않는다. 보호된 workflow는 신뢰하는 위치의 workspace 설정을 `--workspace`로 명시하며 후보 checkout의 설정 자동 발견에 의존하지 않는다. 후보가 설정을 삭제하거나 repo 목록을 줄여도 단일 repo 모드로 전환하지 않고 기존 범위에 대해 누락·의무 변경을 판정한다. 로컬 무설정 결과는 기존 workspace 검증을 대체하지 않는다.

공유 CI의 기준 의무는 보호된 workspace 설정과 각 repo 기준 커밋의 계약에서 가져온다. 필요한 경우만 고급 옵션 `--baseline <snapshot>`으로 승인된 기준 조합을 지정한다. snapshot 자체가 승인을 증명하지 않으며 허용 기준은 CI의 보호된 구성에서 결정한다. 관계·시나리오·정책 삭제·축소는 기존 의무와 비교하고, 명시적 종료에는 이유와 보호된 정책의 처리가 필요하다. 로컬 실행은 기준 의무의 출처를 표시하며 인증된 조직 승인으로 표현하지 않는다.

`--baseline` 사용 시 해당 snapshot의 전체 후보 조합을 이번 실행의 기준으로 삼는다. 명시 `--base`와 함께 사용하면 오류이며 설정의 base 기본값보다 우선한다. baseline에 있던 repo·의무를 현재 설정이 빠뜨리면 자동 제외하지 않고 차단한다. 새 repo 추가는 기준 부재를 표시하고 보호된 정책에 따라 처리한다. `--snapshot` 재현과 `--baseline` 새 비교는 서로 다른 모드이며 함께 지정할 수 없다.

## 첫 버전 범위와 구현 순서

| 우선순위 | 기능 | 완료 기준 |
| --- | --- | --- |
| P0 | 무설정 시작·최소 설정 생성 | 경로만으로 repo별 결과 확인, 설정 하나로 재사용 |
| P0 | repo별 여러 브랜치 결합 | 두 독립 repo·총 세 브랜치, 자동/명시 선택·기준 유지, 원본 무변경 |
| P0 | 자동 snapshot·재현 | ref 이동 후 원래 조합 재현 또는 명확한 객체 누락 오류 |
| P0 | 좁은 discovery·HTTP 계약 | 출처 있는 검토 초안, 별도 소비 기대, 정적 행렬·누락 표시 |
| P0 | 수정 단서·read-only MCP | repo·브랜치·파일·SHA·재검증을 CLI와 에이전트에 동일하게 제공 |
| P0 | 기존 명령 기반 조합 테스트 | 실제 두 repo HTTP 실패→수정→성공, 후보 경로·정리·stale 검증 |
| P0 | CI·연동 예제 | 고정 SHA·보호된 의무·artifact, cmux/tmux/Orca 호출 예제 |
| P1 | watch·설정 관리 개선 | commit 변경 static 검사, 상태 변화 알림, 설정 유지 비용 감소 |
| P1 | 배포 경로 런타임·범용 lifecycle | 필수 중간 조합·예산·readiness·정리 검증. 초기 정적 행렬과 구분 |
| P1 | Pact·기존 실행 환경 adapter | 정확한 버전에 묶인 외부 증거 재사용 |
| P2 | 이벤트·gRPC·Java | 수요에 따라 독립된 계약·실행 지원과 증거 추가 |

P0는 한 번에 출시할 묶음이 아니라 아래 수직 단계로 나눈다. 최종 후보 런타임이 준비되기 전에는 여러 repo의 실제 동작 검증을 지원한다고 표현하지 않는다. 중간 조합 런타임은 P1 완료 전 필수로 요청하면 BLOCKED다.

| 단계 | 전달할 결과 | 다음 단계 조건 |
| --- | --- | --- |
| 0 문제 확보 | 두 사용자/팀의 repo 간 실패·현재 해결 과정 | 실제 재현 후보 확보 또는 대상 가설 수정 |
| 1 선택과 결합 | `--repo`, init, repo별 여러 브랜치, snapshot, CLI/MCP 요약 | 경로 이동·동명 브랜치·dirty 제외·충돌·명시 선택·재현 검증 |
| 2 계약과 수정 안내 | API 하나의 초안·정적 행렬·repair tasks | 방향별 positive/negative, unsupported·의무 삭제·stale 처리; 첫 사용자에게 제공 |
| 3 실제 조합 실행 | 기존 테스트 adapter와 두 repo·세 브랜치 E2E | 실패·수정 성공·timeout·정리·잘못된 소스 사용·증거 무효화 검증 |
| 4 반복 사용과 CI | 파일럿의 실제 변경, 고정 PR SHA, 보호된 정책·연동 예제 | 설정 시간·재사용·오탐·조사 시간으로 다음 투자 결정 |
| 5 수요 기반 확대 | 배포 경로·Pact·watch·언어/실행기 확장 | 반복 사용자의 문제를 해결하는 순서로 선택 |

자동 clone/fetch, 전역 repo 발견, Kubernetes 생성, 자동 배포, DB 안전 판정, 서비스 메시, 새 GUI는 첫 범위에 포함하지 않는다. 필요한 repo와 의존성은 이미 준비된 환경에서 사용한다.

## 현재 코드와 구현 구조

현재 `internal/integration.Options`와 `Preview`는 하나의 root와 base, branches를 받는다. 이를 repo별로 재사용하되 후보 생성과 실행을 필요한 만큼 분리한다. `contracts.Binding`은 repo 상대 경로이고 기존 integration evidence는 단일 candidate commit/tree에 연결된다. path guard를 느슨하게 만들어 다른 repo에 접근하지 않는다.

| 모듈 | 변경 방향 |
| --- | --- |
| 새 `internal/workspace` | 최소 설정·생성·선택 규칙·ID·경로 override·검토용 discovery |
| `internal/integration` | repo별 다중 브랜치 결합 재사용, 후보 수명과 snapshot 재현 지원 |
| 새 `internal/composition` | repo별 후보 묶음·경로 독립 digest·정적 행렬·조합 예산 |
| `internal/contracts` | 제공·소비 문서 호환성 코어와 workspace adapter, 기존 v1 유지 |
| 새 `internal/servicerun` | 좁은 scenario adapter·후보 manifest·프로세스 정리부터 구현 |
| `internal/evidence`, `internal/gate` | 조합 증거 별도 버전 형식, 기존 harness·정책·무효화 재사용 |
| `internal/cli`, MCP | 동일 선택 규칙·요약·repair tasks·repo별 범위 표시 |
| 새 설정/snapshot/report schemas | 단일 repo 형식과 분리, 예제·버전 호환성 검증 |

문서의 JSON·CLI는 인터페이스 계약 초안이다. 단계 1에서 최소 schema와 parser 테스트로 확정하고, 단계 2에서 계약/시나리오 예제를 확장 schema로 검증한다. 미구현 옵션을 현재 사용 안내에 노출하지 않는다.

## 검증 데이터와 채택 지표

필수 회귀는 다음을 포함한다.

- **선택:** 두 repo·세 브랜치, 같은 브랜치명, 중복 common repo, ID 충돌, 명시 선택 대체, 기준만 참여, worktree 추가/삭제, detached HEAD, 모호한 기준, CLI/설정 우선순위.
- **경로·재현:** 설정 상대 경로·공백·경로 이동, `--path`, dirty 제외, ref 이동, 누락 객체, 결합 순서, 재구성 tree 불일치, 같은 조합의 동일 digest.
- **계약:** 응답 필드 삭제, 요청 required 추가, enum·nullable 변화, 호환 필드 추가, 소비자만 변경, 양쪽 변경, 최종 통과·중간 실패, 미지원 construct, 의무 삭제·축소.
- **실행:** 개별 repo 통과·조합 실패, 스키마 동일 금액 오류, 수정 후 성공, 후보 대신 원본 읽기, 준비 실패, 테스트 0개, timeout·취소·남은 프로세스, 소스·정책·scenario 변경과 stale 거부.
- **기존 경험:** 단일 repo CLI·MCP·정책·테스트 선택·실행 유지, 없는 관계를 PASS로 표현하지 않음, 누락 근거가 확인된 실패를 가리지 않음.

초기 사용성 목표는 실적이 아닌 검증할 제품 가설이다. repo와 의존성이 준비된 환경에서 다음을 측정한다.

| 지표 | 초기 목표 또는 판단 방법 |
| --- | --- |
| 최초 repo별 결과 | 첫 명령으로 5분 안에 선택과 검사 범위를 이해 |
| 최초 유용한 repo 간 결과 | 지원하는 두 repo에서 30분 안에 계약 하나 또는 기존 시나리오 연결. 초과 원인 기록 |
| 설정 부담 | 수작업 항목 수·도입 시간·다음 변경에서 설정 유지 시간 |
| 문제 해결 효과 | 기존 CI/수작업 대비 실패 발견·위치 조사·수정 확인 시간 |
| 반복 사용 | 첫 사용 후 2주 내 다른 실제 변경에도 자발적으로 사용하는지, 중단 이유 |
| 확산 | 다른 repo·동료로 추가 도입되는지. 스타 수와 실제 재사용은 별도 지표 |
| 신뢰·비용 | 독립 라벨 기준 누락·오탐·정상 변경의 불필요한 차단, 전체 suite 대비 실행 시간 |

최소 두 팀의 실제 변경에서 비용 감소와 반복 사용이 관측되면 범위를 확장한다. 유지 부담으로 재사용하지 않으면 언어·MSA 기능 추가보다 설정과 결과 설명을 먼저 개선한다. 작은 파일럿을 시장 전체 수요의 증거로 확대하지 않는다. fixture로 외부 성능·정확도를 주장하거나 Radar 출력으로 정답 라벨을 만들지 않는다.

## 포지셔닝과 우선 결정

현재의 “개별 브랜치는 통과하지만 함께 적용하면 깨진다”는 문제를 여러 repo까지 확장한다. 첫 화면에는 두 repo·세 브랜치의 실패와 수정 성공을 짧게 보여주고, 버전 고정·정책·배포 행렬은 필요한 사용자가 더 알아볼 수 있게 둔다.

tmux·cmux·Orca에서 Radar를 호출하고 결과를 확인하는 경로를 제공한다. 사용자가 에이전트 실행 도구를 교체해야 도입할 수 있는 구조를 만들지 않는다. Pact의 버전별 행렬과 `can-i-deploy`를 독창성으로 내세우지 않는다. [Pact 공식 문서](https://docs.pact.io/pact_broker/can_i_deploy)

제품 가설은 **기존 작업 방식에서 적은 설정으로 여러 repo·브랜치를 검사하고, 관련 수정과 재검증까지의 시간을 줄일 수 있다**는 것이다. 첫 구현은 두 repo·세 브랜치·HTTP 계약 하나·실제 시나리오 하나로 흐름을 완성한다. 설정 생성·MCP·수정 안내를 초기 범위에 포함하고, 더 넓은 MSA 운영 검증은 반복 사용 근거에 따라 확장한다.
