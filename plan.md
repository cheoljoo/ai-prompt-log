# apl (AI Prompt Log Viewer) 개발 계획

## 1. 목표

`tig`처럼 터미널에서 동작하는 뷰어 `apl`(AI Prompt Log viewer)을 만든다. **실행 위치에 따라 2단계 또는 3단계로 동작한다.**

- **tig는 2단계**: commit list → commit diff
- **apl의 기본 동작(일반 프로젝트 안에서 실행) — 2단계**: 그 프로젝트 "자기 것만" 보임
  1. **Prompt list** — 현재 프로젝트에서 주고받은 prompt(turn) 목록, 시간 역순
  2. **Prompt + AI 결과** — 선택한 prompt의 원문과 AI 응답(요약/전체) 상세
- **apl이 집계 지점(llm_wiki 프로젝트) 안에서 실행될 때 — 3단계**: 모든 프로젝트를 모아봄
  1. **Project list** — 어떤 프로젝트에서 AI를 썼는지
  2. **Prompt list** — 그 프로젝트에서 주고받은 prompt 목록
  3. **Prompt + AI 결과** — 상세

즉 "project list" 레벨은 llm_wiki 컨텍스트에서만 나타나는 진입 단계이고, 그 외 모든 프로젝트에서는 자기 prompt list부터 바로 시작한다(tig와 동일한 2단계 감각). 단계 전환은 apl이 **현재 작업 디렉터리가 집계 지점(llm_wiki)인지 여부**로 자동 판단한다(§2-1).

tig처럼 단계별로 색을 구분해 어떤 뷰에 있는지, 어떤 종류의 텍스트(사용자 prompt / AI 텍스트 / tool 실행 / 코드)인지 한눈에 구분되게 한다.

1차 대상은 **Claude Code**. (Codex/Cursor 등 다른 AI 도구 로그는 이후 어댑터로 확장 가능하도록 설계만 열어둔다.)

## 2. 핵심 설계 결정: 데이터는 "새로 모으지 않는다"

Claude Code는 이미 세션마다 prompt/응답을 다음 위치에 JSONL로 기록하고 있다 (직접 확인함):

```
~/.claude/projects/<cwd를 인코딩한 디렉터리명>/<session-uuid>.jsonl
```

- 인코딩 규칙: 절대경로의 `/`를 `-`로 치환 (예: `/data01/cheoljoo.lee/code/ai-prompt-log` → `-data01-cheoljoo-lee-code-ai-prompt-log`)
- 한 디렉터리 = 한 "project" (level 1). 프로젝트 안에 세션(uuid).jsonl 파일이 여러 개 있을 수 있음 — 이걸 시간순으로 합쳐서 prompt list(level 2)를 만든다.
- 한 줄(JSON)은 `type` 필드로 구분됨. 우리가 쓰는 핵심 타입:
  - `"type":"user"` — `message.content`에 사용자가 입력한 prompt (문자열 또는 블록 배열)
  - `"type":"assistant"` — `message.content`는 블록 배열(`text`, `thinking`, `tool_use`, `tool_result` 등)
  - 그 외(`mode`, `system`, `cost-state`, `file-history-*`, `bridge-session` 등)는 뷰어에서 무시
- 공통 메타: `timestamp`(ISO8601), `sessionId`, `uuid`/`parentUuid`(스레드), `cwd`, `gitBranch`, `isSidechain`(서브에이전트 여부)

**Prompt 단위 정의**: `type:"user"` 레코드 하나를 prompt 경계로 보고, 다음 `user` 레코드가 나오기 전까지의 연속된 `assistant` 레코드들을 그 prompt에 대한 "AI 결과"로 묶는다. `isSidechain:true`인 레코드(서브에이전트 내부 대화)는 기본은 접어서(collapsed) 보여주고 펼치기 키로 볼 수 있게 한다.

### 2-2. `/clear`·`compact` 시에도 원본이 남는가 — 실측 확인 (결론: 남는다)

실제 jsonl을 조사해 확인한 사실:

- **compact(자동 컨텍스트 부족 압축 / 수동 `/compact`)**: 같은 jsonl 파일, 같은 `sessionId` 안에서 일어난다. `llm_wiki` 세션(5632줄) 실측 결과, 479번째 줄에 "이전 대화 요약" 합성 user 메시지 + 480번째 줄에 `<command-name>/compact</command-name>` 마커가 삽입되지만, **그 앞의 원본 대화 0~478번째 줄은 삭제되지 않고 그대로 남아있었다.** → compact 전/후 원본이 한 파일 안에 전부 존재 → apl이 그대로 파싱하면 자동으로 다 보인다. 별도 처리 불필요.
- **`/clear`**: compact와 다르게 **새 `sessionId`의 새 jsonl 파일**로 넘어간다(실측: `<command-name>/clear</command-name>`로 시작하는 6줄짜리 새 세션 파일 확인). 이전 대화가 있던 jsonl은 삭제되지 않고 같은 프로젝트 디렉터리에 그대로 남는다. → §2에서 이미 "프로젝트 = 여러 세션 jsonl을 시간순 병합"으로 설계했으므로, 이 설계 자체가 `/clear`로 나뉜 세션들을 자동으로 합쳐서 보여준다. 추가 로직 불필요, 기존 설계로 충분.
- **정렬 주의사항**: compact 경계의 timestamp가 항상 순증가하지 않음을 확인함(요약 줄의 timestamp가 직전 줄보다 빠른 경우 존재). Prompt 정렬은 **timestamp가 아니라 파일 내 줄 순서(또는 `uuid`/`parentUuid` 체인)를 1차 기준**으로 삼는다. 세션 간(파일 간) 정렬에는 각 세션의 첫 레코드 timestamp를 쓰되, 세션 내부 순서는 줄 순서를 신뢰한다.

이 설계 덕분에 apl은 **읽기 전용 로그 뷰어**로만 만들면 되고, Claude Code 훅이나 별도 로깅 파이프라인을 새로 만들 필요가 없다. (단, `b2e0d8fb-...jsonl` 처럼 12MB가 넘는 세션 파일도 실제로 존재하므로 — 전체를 메모리에 올리지 않는 스트리밍 파싱 + 인덱스 캐시가 필요하다. §6 리스크 참조.)

### 2-1. 집계(sync) 설계 — 결정됨: "로컬 캐시로 동기화, git 커밋 안 함"

`~/.claude/projects`는 **머신 로컬**이라 다른 머신에서는 안 보인다. 사용자가 이미 `wiki-log`/`wiki-session-export` 스킬로 llm_wiki를 여러 프로젝트의 지식이 모이는 중앙 저장소로 쓰고 있으므로, apl도 같은 위치(llm_wiki)에서 전체를 모아보게 한다. 단, prompt 원문은 민감할 수 있어 **git에는 커밋하지 않는다**.

- `apl sync` (또는 뷰어 기동 시 자동/수동 트리거)가 `~/.claude/projects/**/*.jsonl` 전체를 llm_wiki 저장소 안의 로컬 캐시 디렉터리(예: `llm_wiki/.ai-prompt-log-cache/`)로 미러링한다.
  - 증분 동기화: mtime/size 비교로 변경된 세션 파일만 다시 복사(또는 심링크) — 매번 전체 복사하지 않음
  - `llm_wiki/.gitignore`에 캐시 디렉터리 추가 → git history에는 절대 들어가지 않음
- 여러 머신 간 이동성이 필요하면(llm_wiki 폴더를 백업/rsync 하는 등) 이 캐시 디렉터리도 함께 옮기면 됨 — git이 아닌 별도 수단으로 이동
- apl의 project-list(3단계 진입) 뷰는 `~/.claude/projects`가 아니라 **이 캐시 디렉터리**를 읽는다 → llm_wiki에서 봐도 항상 "마지막 sync 시점" 데이터라는 점을 상태바에 명시(`last synced: N분 전`)
- 일반 프로젝트(2단계 모드)는 캐시를 거치지 않고 자기 자신의 `~/.claude/projects/<encoded-cwd>/` 를 직접 읽는다(sync 불필요, 항상 최신)

## 3. UX / 화면 설계

### 진입 동작 판단
apl 시작 시 현재 디렉터리(또는 상위 git root)가 llm_wiki 저장소인지 확인한다.
- **llm_wiki 안** → Level 1(Project list)부터 시작, 캐시 디렉터리(§2-1) 기준 3단계
- **그 외 모든 프로젝트** → Level 1 생략, 바로 자기 프로젝트의 Prompt list(구 Level 2)부터 시작, `~/.claude/projects/<encoded-cwd>/` 직접 읽음(2단계)

### Level 1 — Project list (llm_wiki 안에서만 표시)
```
 ai-prompt-log        12 prompts   last: 2h ago
 llm_wiki             340 prompts  last: 5m ago
 headless-browser-with-ai  88 prompts  last: 3d ago
```
- 정렬: 최근 활동순(기본), 이름순 토글
- 색: 프로젝트명(bold cyan), prompt 개수(dim), 마지막 활동 시각(초록=오늘, 노랑=이번주, 회색=그 외) — tig의 date coloring과 동일한 감각

### Level 2 — Prompt list (선택한 project 안)
```
 2026-09-16 11:53  main   "apl tool을 만들어주세요..."
 2026-09-15 18:20  main   "plan.md 검토해줘"
 2026-09-14 09:02  fix/x  [subagent] "테스트 실패 원인 분석"
```
- tig의 commit list처럼: 날짜(dim), 브랜치(magenta), prompt 첫 줄 요약(기본색), 서브에이전트/sidechain은 접두 태그(yellow)
- `/` 검색, `n`/`N` 다음/이전 매치 (tig 동일 키)

### Level 3 — Prompt + AI 결과 상세
```
┃ USER  2026-09-16 11:53:07                                   (cyan bar)
┃ apl tool을 만들어주세요...
┃
┃ ASSISTANT                                                    (green bar)
┃ (text 응답)
┃
┃ ▸ TOOL: Bash `ls -la ...`                                    (yellow, 접힘)
┃ ▸ TOOL: Edit plan.md                                         (yellow, 접힘)
```
- USER / ASSISTANT-text / TOOL-call / THINKING(옵션, 기본 숨김) 을 색+아이콘으로 구분
- 긴 응답은 tig의 diff pager처럼 스크롤, `Tab`으로 tool 블록 펼치기/접기

### 공통 키바인딩 (tig 관례 최대한 재사용)
`j/k` 이동, `Enter` drill-down, `q`/`Backspace` 뒤로가기, `/` 검색, `g/G` 처음/끝, `Tab` 블록 접기/펼치기, `Q` 종료

## 4. 아키텍처 (레이어 3개, 언어 무관하게 동일)

1. **Log Source Layer**: 두 모드 지원 — (a) direct 모드: 현재 프로젝트의 `~/.claude/projects/<encoded-cwd>/*.jsonl`만 직접 스캔 (b) aggregate 모드: llm_wiki 로컬 캐시 디렉터리(§2-1) 전체 스캔. `apl sync`가 `~/.claude/projects/**/*.jsonl` → 캐시 디렉터리 증분 미러링 담당
2. **Parse & Model Layer**: JSONL → `Project { path, sessions[] }` / `Prompt { user_turn, assistant_turns[], tool_calls[], timestamp, branch, sidechain }` 모델로 정규화. 성능을 위해 파싱 결과를 프로젝트별 인덱스(경량 캐시, 예: sqlite 또는 jsonl 자체 mtime 기반 캐시)로 저장
3. **TUI Layer**: 실행 디렉터리 판정(§3 "진입 동작 판단")에 따라 2단계/3단계 네비게이션 전환 + 색상 렌더링, 필터(날짜/브랜치/source)·통계 화면·export 포함. Log Source/Parse 계층과는 인터페이스로 분리 (나중에 Codex 등 다른 어댑터를 Parse Layer에 추가하기 쉽게)

## 5. 언어 제안: POC vs Release

| | POC | Release |
|---|---|---|
| 언어 | **Python** | **Go** |
| TUI 라이브러리 | Textual (또는 curses) | bubbletea + lipgloss (Charm) |
| 실행 | `uv run apl` (사용자 CLAUDE.md 지침: python은 `uv`로 실행) | 단일 정적 바이너리 `apl` |
| 이유 | 반복 개발 속도 최고, JSONL/시간 파싱/색상 로직을 빠르게 시행착오 가능, textual이 tig류 3-pane 레이아웃에 적합 | 설치가 `apl` 바이너리 하나로 끝남(런타임 의존성 없음), 대용량 jsonl(수십MB) 스트리밍 파싱 성능, 시작 속도가 tig급으로 빨라야 매번 켜는 CLI 도구로 쓸만함 |
| 배포 | 사내 개발용, `uv tool install` 정도 | `go install`, 정적 바이너리 배포, 추후 homebrew/apt 패키징 여지 |

**권장 진행 방식**: POC(Python)로 3단계 뷰 + 색상 + 키바인딩 + 실제 `~/.claude/projects` 데이터로 검증까지 끝낸 뒤, 검증된 데이터 모델/UX를 그대로 Go로 이식. Parse 로직(JSONL → Prompt 모델)이 핵심 자산이므로 POC 단계에서 이 부분의 스펙(§2)을 문서로 고정해두면 이식이 기계적으로 된다.

## 6. 리스크 / 미해결 이슈

- **대용량 세션 파일**: 실측 결과 12MB짜리 단일 jsonl 존재 확인. 전체 로드 금지 → 스트리밍 파싱 + (mtime, size) 기반 캐시 인덱스 필요
- **sidechain(서브에이전트) 표현**: 기본 접기 + 펼치기 키. 메인 대화 흐름을 방해하지 않게
- **project 매칭**: 같은 물리 디렉터리를 다른 경로(symlink 등)로 열면 다른 project로 잡힐 수 있음 — 1차 버전은 무시하고 알려진 제약으로 문서화
- **tool_result 렌더링**: 매우 긴 커맨드 출력은 요약 + "전체보기" 토글 필요
- **비-Claude Code 어댑터**: 이번 범위 밖, 인터페이스만 열어둠
- **캐시 staleness(§2-1)**: llm_wiki(3단계 모드)는 마지막 `apl sync` 시점 데이터라 최신이 아닐 수 있음 → 상태바에 sync 시각 필수 표시, 오래됐으면(예: 1일 이상) 경고색
- **`--resume` 지원 여부 미검증**: §7에서 제안한 "세션 재개 커맨드 표시" 기능은 Claude Code CLI가 실제로 세션 id 기반 재개를 지원하는지 Agent B 단계에서 먼저 확인 필요

## 7. 참고 도구 조사 결과 및 반영 기능 (Action Items)

아래 5개 도구/서비스를 조사해 apl에 반영할 기능을 추출했다.

| 도구 | 확인된 핵심 기능 | apl 반영 여부 |
|---|---|---|
| [prompt-logger](https://github.com/rotationalio/prompt-logger) | SQLite 저장, `export` CLI로 JSONL 내보내기, namespace(프로젝트)별 구분, 모델/시간 메타데이터 | **v1**: `apl export` 서브커맨드(선택 범위를 JSON/JSONL로 내보내기). 프로젝트=namespace는 이미 §2 설계에 있음 |
| [AI Log Analyzer](https://infinium.tools/tools/ai-log-analyzer/) | 근본원인/패턴 분석, 대용량 파일은 tail/시간범위로 제한 처리 | **v1**: `--since`, `--tail N` 같은 범위 제한 옵션(대용량 jsonl 리스크 대응, §6과 연결). **v2 검토**: 반복되는 유사 prompt/실패 패턴 탐지(인사이트 뷰) — 이번 범위 밖 |
| [AI Prompt History Logger (VSCode)](https://marketplace.visualstudio.com/items?itemName=itsArmanKhan.ai-prompt-history-logger) | source/날짜 필터, 통계(총 prompt 수, 일별 카운트, top files), Markdown/CSV/JSON export, git branch 메타 | **v1**: (a) Prompt list에 날짜범위·브랜치·source(main/subagent) 필터 추가 (b) 간단 통계 화면(`s` 키: 프로젝트별/일별 prompt 수) (c) export 포맷에 Markdown 추가 — **기존 `wiki-session-export` 스킬 출력 형식과 맞추면** llm_wiki에 바로 이어붙이기 좋음 |
| [PromptHistory](https://prompthistory.com/) | 버전관리·태그/카테고리·공유(협업) — 페이지 정보 제한적이라 세부 불확실 | **v2 검토**: prompt 북마크/태그(`*` 키로 즐겨찾기) 정도만 저비용으로 고려, 공유/협업 기능은 범위 밖(개인 로컬 도구) |
| [Chat History Viewer (MCP skill)](https://mcpmarket.com/tools/skills/chat-history-viewer) | 과거 대화 조회로 컨텍스트 복원, 감사(audit)/거버넌스, 여러 AI 도구 연동 | **v1**: Level 3 상세에서 `r` 키로 세션 재개 커맨드(`claude --resume <sessionId>`) 표시/클립보드 복사 — *Claude Code CLI의 `--resume` 옵션 지원 여부는 Agent B에서 실제 검증 필요*. 감사/거버넌스 관점은 §8 `security-review`(읽기 전용, 외부 전송 없음)로 이미 커버. 멀티툴 지원은 §4의 어댑터 확장 포인트로 계속 유지 |

**요약 (v1에 새로 추가되는 것)**: 필터(날짜/브랜치/source), 통계 화면, export(JSON/JSONL/Markdown), 범위 제한 옵션(`--since`/`--tail`), 세션 재개 커맨드 표시.

## 8. 멀티 에이전트 실행 계획 (Intent / Result 명시)

아래 단계는 순차 의존성이 있으므로 각 단계는 이전 단계 Result를 받아 다음 Intent의 입력으로 삼는다. 각 단계는 `Agent` 도구로 fork/새 에이전트에 위임 가능하도록 독립적으로 검증 가능한 Result를 갖는다.

### 8-0. 원칙: "단계마다 계약서" — 프롬프트의 파일화

([ax-hackathon-goover.vercel.app/principles](https://ax-hackathon-goover.vercel.app/principles) 참고 요청됨 — 해당 사이트는 클라이언트 렌더링 SPA라 WebFetch로 원문을 온전히 가져오지 못했으나, 이미 이 환경의 `process-intent`/`herdr-intent` 스킬이 Jira 티켓마다 `intent.md`/`spec.md`/`plan.md`를 만들어 다음 에이전트에게 넘기는 것과 동일한 원칙이므로 동일 방식을 적용함)

각 Agent A~E는 대화 맥락으로만 넘기지 않고, 시작 전에 **그 단계의 계약서 파일**을 `agents/<단계>.md`로 먼저 만든다. 계약서에는 최소한 다음이 들어간다:
- **Intent**: 이 단계가 할 일 (범위 밖인 것도 명시)
- **Input**: 이전 단계의 어떤 산출물(파일 경로)을 입력으로 쓰는지
- **Result(계약)**: 산출물의 정확한 경로/형식과, "이 조건을 만족하면 완료"라는 수용 기준(acceptance criteria)

이렇게 하면 각 에이전트를 독립된 fork/새 세션으로 던져도 대화 이력 없이 계약서 파일만으로 동일하게 재현·재실행할 수 있고, 이후 어떤 근거로 그 단계가 "끝났다"고 판단했는지 감사(audit)가 가능하다. (§7의 chat-history-viewer 관련 "감사/거버넌스" 항목과도 연결됨)

계약서 파일명은 각 에이전트 섹션과 1:1로 맞춘다:
- `agents/A-data-model.md`, `agents/B-poc.md`, `agents/C-poc-review.md`, `agents/D-go-release.md`, `agents/E-packaging.md`

### Agent A — 데이터 모델 스펙 조사
- **Intent**: 실제 `~/.claude/projects/**/*.jsonl` 여러 프로젝트/세션을 샘플링해 §2의 스펙을 검증·보강한다. 등장하는 모든 `type` 값, `content` 블록 타입, sidechain/tool_result 케이스를 표로 정리한다.
- **Result**: `docs/data-model.md` — Prompt/Project 정규화 모델과 파싱 규칙(경계 판정, 예외 케이스) 확정본. 이후 모든 에이전트가 이 문서만 참조하면 되도록 자기완결적으로 작성.

### Agent B — Python POC 구현
- **Intent**: Agent A의 `docs/data-model.md`를 따라 `uv` 기반 Python 프로젝트(Textual)로 3단계 TUI 구현. 색상 규칙(§3)과 키바인딩(§3) 반영. 최소한 read-only, 캐시 없이도 동작(성능 최적화는 후순위).
- **Result**: `poc/` 디렉터리에 `uv run apl` 로 실행 가능한 앱. 실제 사용자의 `~/.claude/projects` 데이터로 3단계 모두 동작 확인한 스크린샷/로그.

### Agent C — POC 검증 및 피드백 (code-review, run 스킬 활용)
- **Intent**: `run` 스킬로 POC를 직접 기동해 golden path(프로젝트 선택→prompt 선택→상세 보기)와 엣지 케이스(대용량 파일, sidechain, 빈 프로젝트)를 수동 확인. `code-review` 스킬로 Parse Layer 로직 리뷰.
- **Result**: `docs/poc-feedback.md` — 발견된 버그/UX 이슈/성능 이슈 목록과 Release 단계에서 고쳐야 할 항목.

### Agent D — Go Release 구현
- **Intent**: Agent A 스펙 + Agent C 피드백을 반영해 Go(bubbletea/lipgloss)로 재구현. 스트리밍 파싱 + 캐시 인덱스 포함. 단일 바이너리 `apl` 빌드.
- **Result**: `cmd/apl` 빌드 산출물, `make build`/`go install` 경로, README 사용법 갱신. 실제 데이터로 POC와 동일한 golden path 재확인.

### Agent E — 패키징 & 문서화
- **Intent**: README.md에 설치법/사용법/키바인딩 표 반영, `security-review` 스킬로 로그 파일(개인정보 포함 가능) 접근 범위 점검(읽기 전용, 외부 전송 없음 확인).
- **Result**: 배포 가능한 README + 보안 점검 결과. 여기서 이슈 없으면 v1 완료.

## 9. 활용 skill

- **run**: Agent C에서 POC/Release 앱을 실제로 기동해 golden path 확인할 때
- **code-review**: Agent C, D에서 Parse Layer/TUI 로직 리뷰(정확성/단순화)
- **security-review**: Agent E에서 로그 파일 읽기 범위·외부 전송 여부 점검 (개인 prompt 로그를 다루므로 필수, 캐시 디렉터리가 git에 안 들어가는지도 함께 점검)
- **init**: 필요 시 프로젝트 CLAUDE.md 초기화(현재는 없음, 필요해지면 사용)
- **process-intent 방식 준용**(§8-0): 별도 스킬 호출은 아니지만, 이 프로젝트 자체가 이미 `process-intent`/`herdr-intent` 스킬이 쓰는 "intent/spec/plan 파일화" 관례를 그대로 따름

## 10. 제안 디렉터리 구조

```
ai-prompt-log/
  plan.md                 (본 문서)
  agents/                  (§8-0, 단계별 계약서 파일)
    A-data-model.md
    B-poc.md
    C-poc-review.md
    D-go-release.md
    E-packaging.md
  docs/
    data-model.md          (Agent A 결과물)
    poc-feedback.md         (Agent C 결과물)
  poc/                      (Agent B, Python)
    pyproject.toml
    apl/
      __init__.py
      source.py             (jsonl 스캔, direct/aggregate 모드)
      sync.py               (apl sync: ~/.claude/projects → llm_wiki 캐시 미러링)
      model.py              (Project/Prompt 정규화)
      tui.py                (Textual 앱, 2/3단계 뷰 전환)
  cmd/apl/                  (Agent D, Go release)
    main.go
  internal/
    source/                 (jsonl 스캔, 스트리밍 파싱)
    sync/
    model/
    tui/                    (bubbletea)
  Makefile
  README.md
```

llm_wiki 저장소 쪽에는 `.ai-prompt-log-cache/`(§2-1 캐시, `.gitignore` 등록)가 추가된다.

## 11. 다음 액션

이 plan.md에 동의하면 **Agent A(데이터 모델 조사)** 부터 시작한다 — 시작 전에 `agents/A-data-model.md` 계약서 파일부터 만든다(§8-0). Agent A 없이 바로 POC로 가면 파싱 규칙이 코드 여기저기 흩어져 Go 이식 때 재조사해야 하므로, 순서를 지키는 걸 권장한다.
