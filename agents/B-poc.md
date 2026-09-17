# Agent B — Python POC 구현 (계약서)

## Intent
`docs/data-model.md`를 따라 `uv` 기반 Python(Textual) 프로젝트로 apl POC 구현.
- `apl`(2단계 direct) / `apl --all`(3단계 aggregate) 플래그로 전환 — `~/.claude/projects`를 항상 직접 읽음, 복사/캐시 없음(§2-1, v2에서 캐시 방식 폐기)
- 색상 구분(USER/ASSISTANT/TOOL/cmd), vi 스타일 키바인딩

## Input
- `docs/data-model.md` (Agent A 결과물)
- plan.md §3 UX, §5 언어 선택

## Result (계약)
- `poc/` 아래 `uv tool install --editable poc/`로 `apl` 커맨드 설치 가능
- `apl` 실행 시 현재 프로젝트 자기 것만 2단계로 보임 (직접 검증)
- `apl --all` 실행 시 위치와 무관하게 `~/.claude/projects` 전체가 3단계로 보임 (직접 검증)
- `apl --help`로 사용법·키바인딩·저장소 주소 확인 가능 (직접 검증)

## 상태: 완료 — 실행 검증 로그

모두 실제 데이터(`~/.claude/projects`, `llm_wiki` 저장소)로 검증함. mock 없음.

1. **direct 모드(2단계), 실제 이 프로젝트(`ai-prompt-log`)에서**
   - `model.load_project()`가 이 대화 세션의 jsonl을 직접 파싱해 prompt 6개(지금 이 turn 포함)를 정확히 복원함을 확인
   - Textual `run_test()` 헤드리스 파일럿으로 `PromptListScreen → Enter → DetailScreen → Escape → PromptListScreen` 전체 네비게이션 통과
   - 버그 1건 발견·수정: Textual 8.2.8에서 `Static`에 raw `rich.Text` 전달 시 `render_strips` 누락으로 크래시 → escape된 Rich markup 문자열로 전환
   - 버그 2건 발견·수정: 헬퍼 메서드명 `_render`가 Textual 내부 private 메서드(`Widget._render()`)와 충돌해 위젯 트리를 건너뛰고 직접 호출됨 → `_build_markup`으로 개명
2. **`apl sync`, 실제 `llm_wiki` 저장소에서**
   - `cd llm_wiki && apl sync` 실행 → `26 file(s) copied, 0 unchanged -> .../llm_wiki/.ai-prompt-log-cache`
   - 캐시 아래 실제 프로젝트 16개 디렉터리 생성 확인(총 86M)
   - `.gitignore`에 `/.ai-prompt-log-cache/` 자동 추가, `git status`에서 캐시 디렉터리가 안 보임을 확인
3. **aggregate 모드(3단계), 실제 `llm_wiki`에서**
   - `source.detect_mode()`가 `llm_wiki`를 `aggregate`로 정확히 판정
   - `ProjectListScreen`에 실제 프로젝트 16개(예: `ai-prompt-log`, `llm_wiki`, `hermes`, `herdr` 등) 표시 확인
   - `ai-prompt-log` 프로젝트로 drill-down → prompt 6개 표시 → 상세까지 진입 → Escape 2번으로 project list까지 정상 복귀
4. **엣지 케이스**: 세션 기록이 전혀 없는 디렉터리(`/tmp`)에서 direct 모드 실행 시 "(no prompts found)" 플레이스홀더만 표시되고 Enter를 눌러도 크래시 없음 확인

## UX 재설계 (사용자 피드백 반영)

최초 구현은 tig처럼 화면 전체를 push/pop하는 방식(Screen 전환)이었으나, 실사용 피드백에 따라 **분할 화면(pane) 방식**으로 전면 재작업함:

- 화면 전환 대신 **한 화면 안에 여러 pane을 나란히 배치**하고, 리스트 pane에서 커서를 움직이면(Enter 없이) 오른쪽 pane이 실시간으로 갱신됨(tig의 main+diff 분할 뷰와 동일한 감각)
  - direct 모드: `[ Prompts | Detail ]` 2-pane
  - aggregate 모드: `[ Projects | Prompts | Detail ]` 3-pane
- vi 스타일 키 전면 지원: `j/k` 위아래, `g/G` 처음/끝, `l`/`Tab` 다음 pane으로 포커스 이동, `h`/`Shift+Tab`/`Escape` 이전 pane으로 포커스 이동
- 포커스된 pane은 굵은 테두리(`$accent`)로 강조되어 어디 있는지 항상 보임
- **`q`가 반응 안 하던 버그 수정**: `q`를 Screen이 아니라 `AplApp.BINDINGS`에 `priority=True`로 등록 — 어떤 pane에 포커스가 있어도 항상 종료됨(이전엔 Screen 레벨에만 바인딩되어 있어 특정 상황에서 안 먹힐 수 있었음)
- **상세(Detail) pane 가독성 개선**: USER/ASSISTANT 구간을 역상 컬러 배지로 구분, TOOL 호출은 도구명 굵게 + 인자별 들여쓰기 한 줄씩, 긴 인자값(예: 파일 write content)은 160자에서 잘라 "(전체 N자)" 표시, 줄바꿈은 `⏎` 기호로 한 줄에 표시해 레이아웃이 안 깨지게 함

헤드리스 파일럿 테스트로 재검증함(`smoke_panes.py`): direct 2-pane j/k/g/G/l/h 네비게이션, aggregate 3-pane에서 프로젝트 전환 시 prompts pane 실시간 갱신, prompts 커서 이동 시 detail pane 실시간 갱신, `q` priority 바인딩 확인 — 전부 실제 데이터(이 대화 세션, llm_wiki 16개 프로젝트)로 통과.

## 추가 버그 2건 (실사용 중 재발견, 근본 원인까지 재조사함)

1. **`Enter`로 오른쪽 pane 포커스 이동 안 됨**: `DataTable`이 `enter` 키를 자기 것(`action_select_cursor`)으로 먼저 가로채서 Screen의 pane-이동 바인딩까지 도달하지 못했음. 키 바인딩이 아니라 `DataTable`이 쏘는 `RowSelected` **메시지**를 Screen에서 받아 `action_focus_next_pane()`을 호출하도록 수정 — `on_data_table_row_selected` 핸들러 추가.
2. **`MarkupError: closing tag '[/yellow]' does not match any open tag`가 계속 재발**: 처음엔 `[subagent]` 태그 하나만 고쳤지만 실사용 중 다시 발생. 재조사 결과 **검증 방법 자체가 잘못돼 있었음** — `rich.text.Text.from_markup()`으로 검증했는데, 실제 `Static` 위젯은 `textual.content.Content.from_markup()`(Textual 8.2.8의 자체 파서)을 씀. 두 파서가 달라서 rich 기준으로는 통과해도 textual 기준으로는 깨지는 입력이 있었음.
   - 근본 원인: `rich.markup.escape()`/`textual.markup.escape()` 둘 다 "태그처럼 안 보이면" 굳이 이스케이프하지 않는 스마트 로직이 있는데, `AskUserQuestion` 같은 도구 호출의 dict/list 인자(대괄호가 중첩된 텍스트, 160자 제한에서 잘리기까지 함)에서 이 휴리스틱이 Textual 8.2.8의 실제 파서와 어긋나 태그 스택이 깨짐
   - 수정: `rich.markup.escape()` 대신 **무조건 모든 `[`를 이스케이프하는 자체 함수**로 교체(`tui.py`의 `escape()`)
   - 재검증: `textual.content.Content.from_markup()`(실제 파서) 기준으로 이 프로젝트 실시간 세션(9개) + llm_wiki 캐시 전체(16개 프로젝트, 560개 prompt) **전수 재검사, 0건 실패**

## vi 스크롤 키 추가 (`Ctrl+F/B/D/U`)

리스트 pane에서는 커서를 페이지 단위로 이동(전체/절반), detail pane(스크롤 영역)에서는 같은 키로 화면을 페이지/절반 스크롤. `hermes` 프로젝트(prompt 34개, llm_wiki 캐시의 실제 데이터)로 헤드리스 검증: `ctrl+f`(0→18), `ctrl+b`(18→0), `ctrl+d`(0→10), `ctrl+u`(10→0), detail pane에서도 스크롤 위치 변화 확인.

## `apl --help` 지원, command palette 키 변경

- `cli.py`를 `argparse` 기반으로 재작성: `apl --help`/`-h`가 즉시 usage/keybinding 표/오픈소스 git 주소를 출력하고 종료(exit 0). 알 수 없는 옵션(`apl --bogus`)은 TUI를 띄우지 않고 에러(exit 2)로 처리 — 이전엔 인자를 무시하고 그냥 TUI가 떠서 non-interactive 환경에서 멈춰 있었음.
- **command palette**: Textual이 모든 앱에 기본 제공하는 기능으로, 앱 안에서 실행 가능한 명령(테마 전환, 스크린샷, 종료 등)을 fuzzy 검색으로 찾아 실행하는 팝업(VSCode의 Ctrl+Shift+P와 같은 개념). 기본 키가 `Ctrl+P`인데 Termius와 충돌한다는 피드백에 따라 **`F1`로 변경**(`AplApp.COMMAND_PALETTE_BINDING = "f1"`). `Ctrl+P`는 이제 아무 것도 하지 않음(헤드리스 테스트로 확인).

## `apl sync`/캐시 방식 완전 폐기 → `apl --all`로 교체 (설계 재검토)

바로 위 "부수 발견"(어디서든 `apl sync`를 실행하면 그 저장소가 실수로 aggregate 모드가 됨)을 겪은 뒤, 사용자가 근본적인 질문을 던짐: "3단계 전체 보기 위해 `~/.claude/projects`를 복사할 필요 없이 그냥 읽으면 안 되나?" — 맞는 지적이었음. 캐시가 실제로 해결해주던 문제(머신 이동성)도, 캐시 자체가 git-ignore된 로컬 파일이라 애초에 제대로 해결하지 못하고 있었음.

**변경 사항**:
- `sync.py` 삭제, `apl sync` 서브커맨드 제거
- `source.detect_mode(cwd, aggregate: bool)`로 단순화 — 디렉터리 위치가 아니라 **`apl --all`(`-a`) 플래그**로만 2/3단계 결정
- `apl --all`은 `~/.claude/projects`를 그 자리에서 직접 읽음(복사/캐시 없음, 항상 최신, 실수로 다른 저장소를 오염시킬 여지 자체가 없어짐)
- `ai-prompt-log`·`llm_wiki` 양쪽에 남아있던 `.ai-prompt-log-cache/`(88M)와 관련 `.gitignore` 항목 정리(삭제)

재검증: `ai-prompt-log` 디렉터리(캐시 없음)에서 `apl --all` 실행 → 실제 `~/.claude/projects` 하위 16개 프로젝트 전부 정상 표시, 프로젝트 개수가 실제 디렉터리 개수와 정확히 일치함을 헤드리스 테스트로 확인. 전체 프로젝트의 실제 prompt 565개에 대해 markup 렌더링도 재검증(0건 실패).

## `apl`이 하위 디렉터리에서 실행되면 아무것도 안 보이던 문제

`ai-prompt-log/agents/`처럼 프로젝트 하위 디렉터리에서 `apl`을 실행하면 로그가 하나도 안 보였음 — Claude Code 세션은 보통 프로젝트 루트에서 시작되지 하위 디렉터리에서 시작되지 않는데, direct 모드가 **정확히 지금 cwd**만 `~/.claude/projects`에서 찾고 있었기 때문. `git status`처럼 상위 디렉터리로 올라가며 로그가 있는 가장 가까운 조상을 찾도록 `source.find_direct_project_dir()` 추가. `agents/`, `poc/`, `poc/apl/`, `docs/` 등에서 실행해도 부모 프로젝트(`ai-prompt-log`)의 실제 로그가 정확히 조회됨을 확인, 진짜로 관련 없는 디렉터리(`/tmp`)는 여전히 빈 상태로 남는 것도 확인.

## Prompt별 토큰 사용량 표시

`assistant` 레코드의 `message.usage`(input/cache_creation/cache_read/output tokens)를 Prompt별로 합산해 `Prompt.total_tokens`에 저장, Prompts pane에 "Tokens" 컬럼(예: `30.3M`)으로 표시하고 Detail pane 헤더에도 표시. 실제 이 세션 데이터(18개 prompt)로 검증 — 캐시 생성 비중이 커서 긴 대화의 prompt는 수백만~수천만 토큰까지 나옴(예: 큰 도구 호출이 많은 turn은 30.3M).

## 알려진 POC 한계 (Agent C/D에서 다룰 것)
- `/` 검색, 날짜/브랜치/source 필터, 통계 화면, export, `--since`/`--tail`, 세션 재개(`--resume`) 표시 — plan.md §7에서 반영하기로 한 기능들은 아직 미구현(POC는 최소 골격만)
- `thinking` 블록은 항상 숨김(토글 없음)
- 세션 파일 파싱을 캐시 없이 매번 새로 함(대용량 프로젝트에서 느릴 수 있음, §6 리스크)

