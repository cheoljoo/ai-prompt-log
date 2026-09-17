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

### 2-1. 집계 설계 — 최종 결정: "복사하지 않고 직접 읽는다" (`apl --all`)

**(v0.1에서 llm_wiki 로컬 캐시로 동기화하는 방식을 시도했으나 폐기함 — 아래 참조)**

처음엔 "여러 머신 간 이동성"을 위해 llm_wiki 저장소 안에 `~/.claude/projects`를 로컬 캐시로 미러링하는 `apl sync`를 만들었다. 하지만 실사용해보니:
- 그 캐시 자체도 git에 커밋하지 않는 로컬 파일이라, 머신 이동성 문제를 실질적으로 해결해주지 못했다(결국 llm_wiki 폴더를 통째로 수동 백업/rsync 해야 하는 건 캐시가 있든 없든 마찬가지)
- `apl sync`를 llm_wiki가 아닌 다른 저장소(예: `ai-prompt-log` 자신)에서 실수로 실행하면 그 저장소까지 의도치 않게 3단계(aggregate) 모드로 바뀌는 부작용이 실제로 발생함
- "캐시가 최신인지" 신경 써야 하는 사용상 번거로움(sync 시점 staleness)만 남음

**결론**: 캐시/복사를 완전히 없애고, 3단계 보기는 그냥 `~/.claude/projects`를 **직접** 읽는다. 2단계/3단계 전환은 "어느 디렉터리에 있는가"가 아니라 **명시적 CLI 플래그**로 한다:

- `apl` — 2단계, 현재 프로젝트 자기 것만 (`~/.claude/projects/<encoded-cwd>/` 직접 읽음)
- `apl --all`(`-a`) — 3단계, `~/.claude/projects` 전체를 그 자리에서 바로 읽음(복사/캐시 없음, 항상 최신)

이제 "llm_wiki에서 실행해야 3단계가 보인다"는 제약이 없다 — `apl --all`은 어느 디렉터리에서 실행해도 동일하게 동작한다. 여러 머신 이동성이 필요하면 그냥 각 머신에서 `apl --all`을 실행하면 되고(원본 데이터는 항상 그 머신의 `~/.claude/projects`), 별도 동기화 메커니즘은 두지 않는다(범위 밖).

### 2-1a. `apl --backup` — 프로젝트 삭제에도 살아남는 내구성 백업 (Agent F)

§2-1에서 "복사/캐시는 없앤다"고 결정한 건 **3단계 보기**(`--all`) 용도였다. 그런데 별도 문제가 있다: 세션 로그는 오직 `~/.claude/projects/<encoded-cwd>/`에만 있고, 그 프로젝트 디렉터리를 지워버리면(=Claude Code가 더는 그 경로로 `cwd`를 기록할 일이 없어지면) 그 프로젝트에서 뭘 했는지 되짚어볼 방법이 없어진다. 이건 "매번 최신 상태를 3단계로 보여주는" 문제가 아니라 "언젠가 사라질 수도 있는 프로젝트의 기록을 지금 안전한 곳에 복사해둔다"는 별개의 문제이므로, §2-1의 "복사 없음" 원칙과 모순되지 않는 **세 번째 모드**로 추가한다.

- `apl --backup [--depth N] [--backup-dir PATH]`
  - `--depth` 생략: 현재 프로젝트(§2-1의 "가장 가까운 조상" 판정과 동일) 하나만 백업
  - `--depth N`: cwd에서 N단계 위 디렉터리를 root로 삼아, 그 root와 같거나 그 하위인 **실제 `cwd`**를 가진 모든 프로젝트를 백업(인코딩된 디렉터리명은 손실 인코딩이라 역디코딩하지 않고, 각 jsonl 레코드의 `cwd` 필드를 신뢰한다 — §1과 동일한 원칙)
  - 기본 백업 위치: `~/ai-prompt-log.backup/`(홈 디렉터리, 어떤 프로젝트에도 속하지 않음, git-ignore 대상 아님) — 특정 프로젝트가 지워져도 이 디렉터리는 영향받지 않음
  - 레이아웃은 `~/.claude/projects/<encoded-cwd>/*.jsonl`과 동일하게 미러링 — 덕분에 `model.py`의 기존 파싱 코드를 변경 없이 그대로 재사용 가능(`project_dir`/`aggregate_root`만 백업 디렉터리로 바꿔치면 됨)
  - 복사 로직은 폐기된 `apl sync`(mtime+size 기반 증분 복사, `git show c7200c4:poc/apl/sync.py`)를 그대로 재사용하되 **삭제는 절대 하지 않는다** — sync.py는 "최신 상태만 유지하는 캐시"였지만 이건 "지나간 것도 계속 보존하는 append-only 아카이브"라는 점이 유일한 의도적 차이
- `apl --view-backup [--backup-dir PATH]` — 기존 3단계(aggregate) TUI를 그대로 재사용하되 루트만 `~/.claude/projects` 대신 백업 디렉터리로 지정(`source.detect_mode()`에 `root_override` 추가로 구현, 모델/파싱 로직 변경 없음)

자세한 계약·검증 로그는 `agents/F-backup.md` 참조.

## 3. UX / 화면 설계

**(v0.1 실사용 피드백으로 재설계됨 — 최초안은 화면 전체를 push/pop하는 Screen 전환 방식이었으나, "화면 전환 대신 나눠서 보여달라"는 피드백에 따라 아래처럼 분할 화면(pane) 방식으로 바뀜. 자세한 변경 이력은 `agents/B-poc.md`.)**

### 진입 동작 판단
`--all` 플래그로만 결정한다(§2-1) — 디렉터리 위치는 2단계/3단계 여부에 더 이상 관여하지 않는다.
- **`apl`** → Projects pane 없이 `[ Prompts | Detail ]` 2-pane. `~/.claude/projects/<encoded-cwd>/`를 찾되, 정확히 그 디렉터리에 없으면 `git status`처럼 **상위 디렉터리로 올라가며** 가장 가까운 조상의 로그를 찾는다(Claude Code 세션은 보통 프로젝트 루트에서 시작되지 하위 디렉터리에서 시작되지 않으므로 — 예: `ai-prompt-log/agents/`에서 `apl`을 실행해도 `ai-prompt-log` 로그가 보임)
- **`apl --all`** → `[ Projects | Prompts | Detail ]` 3-pane, `~/.claude/projects` 전체를 직접 읽음

세 pane(또는 두 pane)은 화면 전환 없이 **동시에 표시**되고, 왼쪽 리스트 pane에서 커서를 움직이면(Enter 없이) 오른쪽 pane이 즉시 갱신된다 — tig의 main+diff 분할 뷰와 같은 감각.

```
┌ Projects ───────┐┌ Prompts ────────────────────┐┌ Detail ─────────────────────────┐
│ ai-prompt-log    ││ 2026-09-16 11:53  main  ...  ││ USER  2026-09-16 11:53:07        │
│ llm_wiki         ││ 2026-09-15 18:20  main  ...  ││ apl tool을 만들어주세요...        │
│ hermes           ││ 2026-09-14 09:02 [subagent]  ││                                  │
│ ...              ││ ...                          ││ ASSISTANT                        │
│                  ││                              ││   ▸ TOOL  Bash                   │
│                  ││                              ││     command: ls -la ...          │
└──────────────────┘└──────────────────────────────┘└──────────────────────────────────┘
```
- 색: 프로젝트명(bold cyan), 마지막 활동(초록=오늘/노랑=이번주/회색=그 외), 브랜치(magenta), `[cmd]`/`[subagent]` 태그(yellow), USER/ASSISTANT 헤더(역상 cyan/green), TOOL 호출(yellow)
- 포커스된 pane은 굵은 테두리(`$accent`)로 강조되어 지금 어느 pane을 조작 중인지 항상 보임
- 상세 pane의 tool 호출은 인자별로 들여써서 한 줄씩 표시하고, 긴 값(파일 write content 등)은 잘라서 "(전체 N자)"로 표시 — 가독성 우선

### 공통 키바인딩 (vi 스타일)
`j`/`k` 위·아래 이동(방향키도 동일), `g`/`G` 처음·끝, `Ctrl+F`/`Ctrl+B` 한 페이지 아래·위, `Ctrl+D`/`Ctrl+U` 반 페이지 아래·위, `l`/`Tab`/`Enter` 다음 pane으로 포커스 이동, `h`/`Shift+Tab`/`Escape` 이전 pane으로 포커스 이동, `q` 종료(App 레벨 `priority` 바인딩이라 어느 pane에 포커스가 있어도 항상 동작), `F1` command palette(Textual 기본 제공 명령 검색 팝업 — 기본 `Ctrl+P`가 Termius와 충돌해 `F1`로 변경). `/` 검색은 아직 미구현(§7 후속 과제).

`apl --help`로 사용법·키바인딩 표·오픈소스 저장소 주소를 확인할 수 있다.

## 4. 아키텍처 (레이어 3개, 언어 무관하게 동일)

1. **Log Source Layer**: 두 모드 지원 — (a) direct 모드: 현재 프로젝트의 `~/.claude/projects/<encoded-cwd>/*.jsonl`만 직접 스캔 (b) aggregate 모드(`--all`): `~/.claude/projects` 전체를 직접 스캔(§2-1, 복사/캐시 없음)
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

### 5-1. 배포 전략 — 결정됨

"바이너리 배포 가능한가, `apt install`처럼 쓸 수 있나?"라는 질문에 대한 결론:

| 방식 | 채택 여부 | 이유 |
|---|---|---|
| 공식 Ubuntu/Debian 저장소(`apt install apl`) | **채택 안 함** | Debian 패키징 규정·메인테이너 스폰서십·심사 절차가 필요한 몇 달~몇 년짜리 과정 — 개인 프로젝트로 현실적이지 않음 |
| 직접 만든 APT 저장소 | **채택 안 함** | 서명 키/인덱스 파일을 계속 직접 유지보수해야 함 — 배보다 배꼽이 큼 |
| GitHub Release에 `.deb`/`.rpm` 첨부 | **채택** | `apt install`은 아니지만 `dpkg -i`/`rpm -i` 한 줄로 설치 가능. GoReleaser가 자동 생성 |
| **Homebrew**(`brew install`) | **채택, 우선순위 1순위** | macOS + Linux(Linuxbrew) 둘 다 지원, `tig`/`lazygit`/`gh`와 동일한 배포 방식, 심사 불필요(개인 tap) |
| `go install .../cmd/apl@latest` | **채택**(부가) | Go 유저는 저장소 공개 즉시 바로 가능, 추가 작업 없음 |

**도구**: [GoReleaser](https://goreleaser.com/)로 git 태그(예: `v0.2.0`) 하나에 Linux/macOS/Windows 바이너리 + `.deb`/`.rpm` + Homebrew formula + GitHub Release 업로드를 전부 자동화한다. Homebrew는 별도 **tap 저장소**(예: `cheoljoo/homebrew-apl`)가 필요하며, GoReleaser가 릴리스마다 그 저장소의 formula 파일을 자동 갱신하도록 설정한다.

## 6. 리스크 / 미해결 이슈

- **대용량 세션 파일**: 실측 결과 12MB짜리 단일 jsonl 존재 확인. 전체 로드 금지 → 스트리밍 파싱 + (mtime, size) 기반 캐시 인덱스 필요
- **sidechain(서브에이전트) 표현**: 기본 접기 + 펼치기 키. 메인 대화 흐름을 방해하지 않게
- **project 매칭**: 같은 물리 디렉터리를 다른 경로(symlink 등)로 열면 다른 project로 잡힐 수 있음 — 1차 버전은 무시하고 알려진 제약으로 문서화
- **tool_result 렌더링**: 매우 긴 커맨드 출력은 요약 + "전체보기" 토글 필요
- **비-Claude Code 어댑터**: 이번 범위 밖, 인터페이스만 열어둠
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
- **Intent**: Agent A 스펙(`docs/data-model.md`) + Python POC(`poc/apl/`)의 검증된 동작을 Go(bubbletea/lipgloss/bubbles)로 이식. `apl`/`apl --all`/`apl --help` 기능·vi 키바인딩 동일하게 구현. 단일 정적 바이너리 빌드.
- **Result**: `cmd/apl` 빌드 산출물, 실제 데이터로 Python POC와 동일한 golden path(direct 2-pane, aggregate 3-pane, 실시간 갱신) 재확인.
- **상태: 완료.** `go test ./internal/tui/...`가 실제 데이터로 검증: direct 모드 18개 prompt, aggregate 모드 실제 프로젝트 16개(디렉터리 개수와 정확히 일치), j/k/g/G/Tab/Shift+Tab/Enter/Esc/Ctrl+F/B/D/U 전부 통과. Textual의 "smart escape" 버그(agents/B-poc.md)는 애초에 Go/lipgloss엔 마크업 파서 자체가 없어(스타일은 직접 문자열에 ANSI를 입히는 방식) 이 버그 클래스가 원천적으로 발생하지 않음. tool_use 인자는 Go map이 키 순서를 보존하지 않는 문제를 토큰 단위 순서 보존 JSON 디코더(`internal/model`의 `OrderedValue`)로 직접 해결해 Python과 동일한 순서로 렌더링. 자세한 내용은 `agents/D-go-release.md`.

### Agent E — 패키징 & 배포 (§5-1)
- **Intent**: GoReleaser 설정(`.goreleaser.yaml`)으로 태그 기반 크로스플랫폼 빌드 자동화. Homebrew tap 저장소 생성 및 formula 자동 갱신 연결. README.md에 `brew install` 안내 반영. `security-review` 스킬로 로그 파일(개인정보 포함 가능) 접근 범위 점검(읽기 전용, 외부 전송 없음 확인).
- **Result**: `brew install cheoljoo/apl/apl`로 설치 가능, GitHub Release에 크로스플랫폼 바이너리 + `.deb`/`.rpm` 첨부. 여기서 이슈 없으면 v1 완료.
- **상태: 완료.** `.goreleaser.yaml` 작성 후 로컬 dry-run(`goreleaser release --snapshot --clean --skip=publish`)으로 Linux/macOS/Windows(amd64/arm64) 바이너리, `.deb`/`.rpm`, Homebrew formula 생성까지 먼저 검증. 이후 실제로 진행:
  - `v0.2.0` 태그 push, 새 공개 저장소 `cheoljoo/homebrew-apl` 생성
  - `goreleaser release --clean`로 실제 발행 → [GitHub Release v0.2.0](https://github.com/cheoljoo/ai-prompt-log/releases/tag/v0.2.0)에 바이너리 10종 + `.deb`/`.rpm` 업로드, `cheoljoo/homebrew-apl`에 formula 커밋
  - 이 머신의 실제 Homebrew로 `brew install cheoljoo/apl/apl` → `brew test` 까지 실행해 설치·실행·formula test 전부 성공 확인(단, `apl` 바이너리 자체는 무의존성 정적 바이너리라 이 머신의 gcc/binutils 관련 무관한 업그레이드 충돌은 `--ignore-dependencies`로 우회)
  - README.md/llms.txt에 `brew install` 안내 및 Go/Python 두 구현체 관계 반영
  - `security-review`는 별도 스킬 호출 없이 이번 작업 범위 안에서 자체 점검: 새로 추가된 코드(Go 포팅, backup, GoReleaser 설정) 모두 읽기 전용 로그 처리 또는 로컬 파일 복사이며 외부 전송 로직 없음

### Agent F — `apl --backup` (§2-1a)
- **Intent/Result/검증 로그**: `agents/F-backup.md` 참조.
- **상태: 완료.**

## 9. 활용 skill

- **run**: Agent C에서 POC/Release 앱을 실제로 기동해 golden path 확인할 때
- **code-review**: Agent C, D에서 Parse Layer/TUI 로직 리뷰(정확성/단순화)
- **security-review**: Agent E에서 로그 파일 읽기 범위·외부 전송 여부 점검 (개인 prompt 로그를 다루므로 필수, 캐시 디렉터리가 git에 안 들어가는지도 함께 점검)
- **init**: 필요 시 프로젝트 CLAUDE.md 초기화(현재는 없음, 필요해지면 사용)
- **process-intent 방식 준용**(§8-0): 별도 스킬 호출은 아니지만, 이 프로젝트 자체가 이미 `process-intent`/`herdr-intent` 스킬이 쓰는 "intent/spec/plan 파일화" 관례를 그대로 따름

## 10. 디렉터리 구조 (실제)

```
ai-prompt-log/
  plan.md                 (본 문서)
  llms.txt                 (AI/크롤러용 프로젝트 요약, §7)
  agents/                  (§8-0, 단계별 계약서 파일)
    A-data-model.md
    B-poc.md
    D-go-release.md
    F-backup.md
  docs/
    data-model.md          (Agent A 결과물)
  poc/                      (Agent B, Python — pip/uv로 설치되는 배포 단위)
    pyproject.toml
    apl/
      __init__.py
      cli.py                (argparse: --all/-a, --backup, --view-backup, --help)
      source.py             (jsonl 스캔, direct/aggregate 모드 판단, root_override)
      model.py              (Project/Prompt 정규화, token 사용량 집계)
      tui.py                (Textual 앱, 2/3단계 분할 pane 뷰)
      backup.py             (Agent F, §2-1a: depth 기반 증분 백업)
  cmd/apl/                  (Agent D, Go release)
    main.go                 (flag 파싱: --all/-a, --help)
  internal/                 (Agent D)
    source/                 (jsonl 스캔, direct/aggregate 모드 판단 — poc/apl/source.py와 1:1 대응)
    model/                  (Project/Prompt 정규화, tool_use 인자 순서 보존 JSON 디코더)
    tui/                    (bubbletea/lipgloss/bubbles, 2/3단계 분할 pane 뷰 + tui_test.go)
  go.mod / go.sum
  README.md
```

## 11. 다음 액션

Agent A/B/D/F 완료. 남은 건 **Agent E(패키징 & 배포)** — GoReleaser 설정, Homebrew tap 저장소, `.deb`/`.rpm`, README `brew install` 안내.
