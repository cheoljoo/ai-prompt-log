# Agent D — Go Release 구현 (계약서)

## Intent
`docs/data-model.md` + 검증된 Python POC(`poc/apl/`)의 동작을 Go로 이식한다.
- `apl`(direct, 2-pane) / `apl --all`(aggregate, 3-pane) 동일하게 동작
- `apl` 하위 디렉터리에서도 상위로 올라가며 로그 탐색(source.find_direct_project_dir와 동일 로직)
- vi 스타일 키(j/k/g/G/Ctrl+F/B/D/U/h/l/Tab/Enter/q), 분할 pane + 실시간 미리보기
- `apl --help`로 사용법/키바인딩/저장소 주소 출력
- Static 렌더링·markup escape 버그(§ agents/B-poc.md) 재발하지 않도록 Go TUI 라이브러리(bubbletea/lipgloss)의 텍스트 렌더링 방식에 맞게 새로 설계(Python의 "무조건 escape" 교훈을 Go에도 적용 — lipgloss는 ANSI 스타일링이라 Rich/Textual류 markup 파서 자체가 없어 이 클래스의 버그가 원천적으로 없음)

## Input
- `docs/data-model.md` (Agent A)
- `poc/apl/*.py` (Agent B, 검증된 참조 구현)
- `agents/B-poc.md` (실사용 중 발견한 버그와 수정 내역 — 같은 실수 반복 방지)

## Result (계약)
- `cmd/apl/main.go` + `internal/{source,model,tui}` 빌드 가능, `go build`로 단일 바이너리 생성
- 실제 `~/.claude/projects` 데이터로 direct/aggregate 모두 골든 패스 확인(Python POC와 동일 결과)
- 빌드된 바이너리를 로컬에서 실행해 스모크 테스트 통과

## 상태: 완료 — 실행 검증 로그

Go 1.23.4(공식 바이너리 배포판 직접 설치 — 이 환경의 Homebrew(Tier 3 리눅스)는 Go를 소스부터 빌드하려다 실패해서 우회함), 의존성: `bubbletea`/`lipgloss`/`bubbles`.

### 구조
- `internal/source`: `poc/apl/source.py`와 1:1 대응(`EncodePath`, `FindDirectProjectDir`, `DetectMode`, `ListSessionFiles`, `ListProjectDirs`)
- `internal/model`: `poc/apl/model.py`와 1:1 대응. **Go map은 키 순서를 보존하지 않아서** `tool_use` 인자를 Python dict와 동일한 순서로 보여주려면 표준 `encoding/json`의 `map[string]interface{}`로는 불가능 — `json.Decoder`의 토큰 스트림을 직접 재귀 파싱하는 `OrderedValue`/`KV` 타입을 만들어 모든 중첩 레벨에서 키 순서를 보존. `PyRepr`/`FormatTopLevel`로 Python의 `str(v)`/`repr(v)` 표시 관례를 그대로 재현(top-level 문자열은 따옴표 없이, 리스트/딕트는 Python repr 스타일).
- `internal/tui`: bubbletea Model. `table.Model`/`viewport.Model`의 커서 이동 메서드(`MoveDown`/`MoveUp`/`GotoTop`/`GotoBottom`/`PageDown`/`PageUp`/`HalfPageDown`/`HalfPageUp`/`LineDown`/`LineUp`)를 직접 호출해 vi 키를 구현 — 각 위젯의 기본 `Update()`/`KeyMap`은 쓰지 않고 완전히 우리가 커서 상태를 소유(Python POC와 동일한 커스텀 키 우선 철학).
- **Textual의 "smart escape" 버그 클래스(agents/B-poc.md)가 Go/lipgloss에는 원천적으로 없음**: lipgloss는 마크업 문자열을 파싱하지 않고 `Style.Render(plainText)`로 직접 ANSI를 입히는 방식이라, 애초에 "이스케이프를 깜빡하면 태그 스택이 깨지는" 클래스의 버그 자체가 불가능한 아키텍처.

### 검증 (`go test ./internal/tui/... -v`, 실제 데이터, mock 없음)
- `TestDirectModeRealData`: 이 프로젝트 실제 prompt 18개 로드, j/k 커서 이동, Enter로 detail pane 포커스 이동 + 실제 콘텐츠 렌더링 확인, Esc로 복귀
- `TestAggregateModeRealData`: 실제 `~/.claude/projects` 프로젝트 16개(디렉터리 개수와 정확히 일치), 프로젝트 전환 시 prompts pane 실시간 갱신, Tab/Shift+Tab으로 Projects→Prompts→Detail→Prompts→Projects 포커스 체인 확인
- `TestPagingAndHalfPaging`: 실제 프로젝트 중 prompt 10개 넘는 것 골라 Ctrl+D/Ctrl+U 반페이지 이동 검증
- `TestQuit`: `q` → `tea.Quit` 커맨드 반환 확인
- `go build ./cmd/apl` 성공, `apl --help` 실제 실행해 출력 확인(사용법/플래그/키바인딩/저장소 URL)

### 범위 밖(v1 이식에서 제외, 추후 검토)
- `apl --backup`/`--view-backup`(Agent F, Python 전용으로 먼저 나온 기능) — Go 쪽 포팅은 후속 과제
- `apl sync` — 이미 폐기된 기능이라 애초에 이식 대상 아님
