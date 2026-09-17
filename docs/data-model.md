# apl 데이터 모델 스펙 (확정본)

Agent A 조사 결과. 이후 모든 구현(Agent B, D)은 이 문서만 참조하면 된다.

## 1. 경로 인코딩

Claude Code는 세션 로그를 다음 위치에 저장한다:

```
~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
```

`<encoded-cwd>`는 프로젝트 절대경로에서 영숫자가 아닌 모든 문자(`/`, `.`, `_` 등)를 `-`로 치환한 것이다.
예: `/data01/cheoljoo.lee/code/llm_wiki` → `-data01-cheoljoo-lee-code-llm-wiki`

이 인코딩은 손실이 있어(`-`와 원래 구분자를 구별 불가) **역디코딩하지 않는다.** 대신 각 jsonl 레코드에 있는 `cwd` 필드(원본 절대경로 그대로)를 읽어 프로젝트 표시 이름으로 사용한다.

## 2. 레코드 타입

한 줄(JSON)의 `type` 필드로 구분:
- `user` — `message.content`에 사용자 prompt. `content`는 문자열(실제 입력) 또는 블록 배열(tool_result 등)
- `assistant` — `message.content`는 블록 배열. 블록 `type`: `text`, `thinking`, `tool_use`, (그 외 `tool_result`는 다음 user 레코드 쪽에 옴)
- 그 외(`mode`, `system`, `cost-state`, `file-history-*`, `bridge-session`, `permission-mode`, `attachment`, `ai-title` 등) — 뷰어에서 무시

공통 메타: `timestamp`(ISO8601), `sessionId`, `uuid`/`parentUuid`, `cwd`, `gitBranch`, `isSidechain`, `isMeta`

## 3. Prompt 경계 판정 규칙

레코드가 다음을 모두 만족하면 새 Prompt의 시작이다:
- `type == "user"`
- `isMeta`가 true가 아님
- `message.content`가 **문자열**(리스트가 아님) — tool_result를 담은 user 레코드는 `content`가 리스트이므로 자동 제외됨

다음 Prompt 경계가 나올 때까지의 연속된 `assistant` 레코드들이 그 Prompt의 "AI 결과"다.

`<command-name>/clear</command-name>`, `<command-name>/compact</command-name>`, 자동 compact 요약("This session is being continued...")도 위 규칙을 만족하는 문자열 content이므로 **그대로 하나의 Prompt로 잡힌다** — 오히려 이 편이 사용자가 원한 "clear/compact 전후 흐름이 다 보이는" 요구에 맞아 의도적으로 유지한다. 표시할 때 이런 항목은 `[cmd]` 태그를 붙여 구분한다.

## 4. Assistant 블록 처리

- `text` → 그대로 표시
- `thinking` → 기본 숨김(POC는 아예 생략, 필요시 토글은 향후 과제)
- `tool_use` → `TOOL: <name> <input 요약>` 형태로 축약 표시

## 5. `/clear` · `compact` 실측 결과 (plan.md §2-2 근거)

- **compact**(자동/수동): 같은 jsonl, 같은 `sessionId` 안에서 요약 레코드가 삽입되지만 **원본 레코드는 삭제되지 않고 그대로 남음**. 추가 처리 불필요.
- **`/clear`**: **새 `sessionId`의 새 jsonl 파일**로 넘어감. 이전 파일은 그대로 남음 → 프로젝트 단위로 여러 세션 파일을 병합하면 자동으로 커버됨.

## 6. 정렬 기준

- 세션 내부: **파일 내 줄 순서**를 신뢰(timestamp가 compact 경계에서 역전되는 사례 실측 확인됨)
- 세션 간(여러 jsonl 병합 시): 각 세션의 첫 유효 레코드 `timestamp` 기준으로 세션을 정렬한 뒤, 세션 내부는 줄 순서 유지

## 7. Project 단위(집계 모드)

- 집계 모드에서 "한 프로젝트" = 캐시 디렉터리 아래 한 하위 디렉터리(= 원래 `~/.claude/projects/<encoded-cwd>/`와 동일 구조)
- 표시 이름은 그 프로젝트의 아무 레코드에서나 읽은 `cwd` 필드의 마지막 path segment 사용
