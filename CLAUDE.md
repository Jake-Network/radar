# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

이 저장소의 작업 계약은 `AGENTS.md` 하나로 관리한다. Claude Code도 아래 import로 같은 내용을 그대로 읽는다. 규칙을 바꿀 때는 이 파일이 아니라 `AGENTS.md`를 수정한다.

@AGENTS.md

## Claude Code에만 해당하는 차이

- `AGENTS.md`의 "subagent"는 Claude Code에서는 Agent 도구의 subagent를 뜻한다. OMX/Codex 전용 지시는 해당 런타임이 없으면 무시한다.
- `radar setup --agent claude`가 사용자 프로젝트에 설치하는 skill/hook/MCP 설정의 원본은 `internal/onboarding/assets/claude.md`, `stop.sh`와 `integrations/claude-code/`다. 이 저장소 자체의 Claude Code 설정과 혼동하지 않는다.
