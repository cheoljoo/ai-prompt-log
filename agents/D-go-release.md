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

### `apl --backup`/`--view-backup` 추가 이식 (Agent F 기능의 Go 포팅)

처음엔 범위 밖으로 미뤄뒀으나("Python code와 같은 동작을 하도록 go도 변경해줘" 요청에 따라) 이어서 포팅함.

- `internal/backup`: `poc/apl/backup.py`와 1:1 대응(`ResolveDepthRoot`, `FindProjectsByCwdAncestry`/`isRootOrDescendant` — `filepath.Rel` 기반으로 Python의 `root in p.parents` 판정과 동일하게 구현, `SelectProjects`, `CopyProject`, `Run`, `FormatSummary`)
- `cmd/apl/main.go`: `--backup`/`--view-backup`/`--depth`/`--backup-dir` 플래그 추가. `--depth`는 Go `flag` 패키지에 "미지정" 상태가 없어 `flag.Visit`으로 실제 지정 여부를 판별해 Python의 `depth: int | None` 의미를 재현. `--all`/`--backup`/`--view-backup` 상호배타 검증 등 Python argparse와 동일한 에러 메시지·exit code(2)
- `internal/model.FindCwdField`를 export해서 backup 패키지가 재사용(Python의 `model._find_cwd_field`와 동일 위치)

### 검증 (실제 데이터, mock 없음)
- `go test ./internal/backup/...`: `SelectProjects`(depth=nil→1개 현재 프로젝트, depth=1→실제 `~/.claude/projects` 16개 전부와 정확히 일치), `CopyProject` 증분 동작(최초 복사/재실행 unchanged/내용 변경 시 updated/**원본 삭제해도 백업은 그대로 남는 것**까지 임시 디렉터리로 검증)
- `go test ./internal/tui/...`의 `TestViewBackupRealData`: 실제 백업 디렉터리(`--backup --depth 1`로 만든 16개 프로젝트, 91M)를 `--view-backup`으로 열어 프로젝트 16개, 첫 프로젝트 prompt 정상 로드 확인
- CLI 실제 실행: `apl --backup --depth 1 --backup-dir <실제경로>` → `16 project(s), 26 copied` 확인 후 재실행 시 `26 unchanged`(Python과 동일한 결과), `apl --depth 1`(단독)과 `apl --all --backup`(동시 지정) 둘 다 exit 2로 정상 거부

이제 Go/Python 두 구현이 완전히 동일한 기능 집합을 가짐(단, Python의 `F1` command palette는 Textual 프레임워크가 공짜로 주는 chrome이라 이식 대상에서 제외 — README에 명시).

## 성능 비교 조사 (사용자 요청) — Go가 Python보다 느렸던 실제 버그 3건 발견·수정

"Go/Python 응답 속도·CPU 사용률·실행 속도를 비교해달라"는 요청에 실제 이 머신의 `~/.claude/projects`(16개 프로젝트, 104M) 전체를 로딩하는 동일 작업(`apl --all` 시작 시 하는 일)을 `/usr/bin/time -v`로 측정. **처음 측정에서 Go가 Python보다 5배 이상 느린 게 드러나서**(4.4s vs 0.87s) `go tool pprof`로 원인을 찾아 고침 — plan.md §5에서 Go를 고른 이유("대용량 jsonl 스트리밍 파싱 성능")가 실제로는 거짓이었던 것.

**발견한 버그 3건** (전부 실제 프로파일링으로 근거 확인):
1. **`sort.Slice` 비교 함수 안에서 파일을 다시 읽음**: `BuildPrompts`가 세션 파일을 시간순 정렬할 때 `sessionStartTimestamp(files[i])`를 비교 함수 **안에서** 호출 — `sort.Slice`는 O(n log n)번 비교 함수를 호출하므로 파일 하나당 여러 번 재파싱됨. 정렬 전에 타임스탬프를 한 번씩만 계산해 캐싱하도록 수정.
2. **조기 종료가 안 되는 iterator**: `iterRecords`가 콜백 기반(push-style)이라 `sessionStartTimestamp`/`FindCwdField`처럼 "첫 값만 필요한" 호출도 파일 전체를 끝까지 읽었음(Python은 제너레이터라 `for rec in _iter_records(path): return ts`가 자연스럽게 즉시 멈춤). `yield` 콜백이 `bool`을 반환해 `false`면 스캔을 중단하도록 변경.
3. **`encoding/json.Decoder`의 이중 스캔 비용**: `tool_use` 인자의 키 순서를 보존하려고 `Decoder.Token()`을 재귀적으로 쓴 게 Go 표준 라이브러리의 알려진 함정이었음 — `Token()`/`Decode()`는 호출마다 남은 스트림 전체를 `checkValid`로 재검증함(pprof에서 `checkValid`+`skip`+`stateInString`이 CPU의 85%+ 차지). 순수 바이트 스캔으로 top-level 키 순서만 뽑고(`scanTopLevelKeyOrder`), 값 디코딩은 `encoding/json` 대신 **`github.com/goccy/go-json`**(드롭인 호환, 훨씬 빠른 서드파티 구현)로 교체.

**결과**: 4.4s → 1.6s(버그 1·2) → 0.62s(버그 3, goccy/go-json). `go test ./...` 전체 재통과(테스트 시간도 15s→2.5s로 같이 줄어듦), markup/토큰/백업 등 기존 검증 전부 그대로 유효.

### 최종 비교 수치 (이 머신, 실제 데이터, `/usr/bin/time -v`)

| 항목 | Go | Python | 비고 |
|---|---|---|---|
| `apl --all` 전체 로딩(16개 프로젝트, 104M, 실측) wall-clock | **0.62s** | 0.87s | Go가 약 30% 빠름 |
| 위 작업 CPU 시간(user+sys) | 0.9s | 0.86s | 비슷함(Go는 GC 등으로 여러 코어 씀, 145%대 CPU) |
| 위 작업 최대 메모리(RSS) | **15~17MB** | 30MB | Go가 약 절반 |
| `apl --help` 프로세스 시작 시간(10회 평균) | **7.3ms** | 43.3ms | Go가 약 6배 빠름(인터프리터 기동 오버헤드 없음) |
| 배포 바이너리/런타임 크기 | 5.8MB 단일 바이너리 | 16MB(venv, Python 인터프리터+의존성 별도 필요) | |

결론: 초기 구현은 실제로 Go가 더 느렸지만(표준 라이브러리 JSON 스트리밍 오용 + 알고리즘 버그), 근본 원인을 고치고 나니 시작 속도·메모리·데이터 로딩 전부 Go가 앞섬 — plan.md §5의 원래 설계 근거가 사후적으로 검증됨.

## `--version`/`-v`, Space 키, Detail pane USER→FINAL-RESULT→ASSISTANT 재구성 (Python과 동일하게)

- `main.go`에 `version` 패키지 변수 추가, `.goreleaser.yaml`의 `-X main.version={{.Version}}`이 실제로 주입되도록 함. `go build`/`go install`처럼 ldflags 없이 빌드될 때는 `runtime/debug.ReadBuildInfo()`로 폴백(실측: git 태그 상태에서 그냥 `go build`만 해도 "v0.2.2+dirty"를 정확히 잡아냄). `--help` 하단에도 버전 표시.
- Space 키(`tea.KeyMsg.String()`이 `" "`(공백 문자 그대로)임을 실측 확인 후) `ctrl+f`와 동일하게 페이지다운 처리.
- `formatPromptDetail`을 Python과 동일하게 `USER → FINAL-RESULT → ASSISTANT` 순서로 재구성(`finalResultText`: 블록 리스트 끝에서부터 연속된 text 블록만 역순으로 모음).
- `go test ./internal/tui/...`에 `TestSpaceAndGRealData`, `TestFinalResultSectionRealData` 추가, 실제 데이터로 순서·커서 이동 검증 통과.


## PR merge → 자동 버전 bump + 배포 (release-please + GoReleaser CI)

"PR을 merge하면 자동으로 새 버전이 만들어져 배포되게 할 수 있는가"라는 요청에 따라, 매번 merge마다 바로 릴리스하는 대신 **변경사항을 하나의 "Release PR"에 모아뒀다가, 그 Release PR을 merge하는 시점에만 실제 배포**가 일어나도록 구성함([release-please](https://github.com/googleapis/release-please) 방식).

- `.github/workflows/release-please.yml`: `main` push마다 실행. `release-please-config.json`(`release-type: simple`, `poc/pyproject.toml`의 `[project].version`을 `extra-files`로 같이 bump)과 `.release-please-manifest.json`(현재 버전 추적, `0.2.3`에서 시작)을 읽어 Release PR을 열거나 갱신. Go 쪽은 버전이 파일에 저장되지 않고(`main.go`의 `version` 변수는 항상 빌드 시점에 goreleaser가 태그로 주입) bump 대상 파일이 필요 없음.
- **커밋 메시지 컨벤션과의 충돌**: 개별 git 커밋 메시지는 전역 CLAUDE.md 규칙대로 `[AGILEDEV-1134] feat: ...`처럼 Jira 티켓이 맨 앞에 오는데, release-please는 conventional commits 규칙상 `feat:`/`fix:`가 문자열 맨 앞이어야 타입을 인식함. 그래서 **이 저장소는 PR을 "Squash and merge"로만 병합**하고(merge commit/rebase는 저장소 설정에서 비활성화 예정), **PR 제목을 `feat: ...`/`fix: ...`로 시작**하는 컨벤션을 쓰기로 함 — 개별 커밋의 티켓 prefix 습관과 Jira 코멘트 자동화는 브랜치 안에서 그대로 유지되고, `main`에 남는 squash 커밋(=PR 제목)만 conventional commits 형식을 따름.
- `.github/workflows/release.yml`: release-please가 만든 `v*` 태그 push에 반응해 `goreleaser release --clean` 실행 — GitHub Release 아티팩트(tar.gz/deb/rpm/checksums)를 release-please가 만든 릴리스에 추가하고, Homebrew tap(`cheoljoo/homebrew-apl`) formula도 같이 갱신. release-please가 만드는 기본 GitHub Release와 태그 이름이 겹치는 게 아니라, goreleaser가 **같은 태그의 기존 release에 아티팩트를 append**하는 방식이라 충돌 없이 동작(goreleaser 공식 문서의 release-please 연동 패턴).
- `homebrew-apl`은 `ai-prompt-log`와 별개 저장소라 기본 `GITHUB_TOKEN`으로는 push 불가 — 저장소 Actions secret `HOMEBREW_TAP_GITHUB_TOKEN`(fine-grained PAT, `homebrew-apl`에 대해 `Contents: Read and write`만 부여)을 만들어 `.goreleaser.yaml`의 `brews[0].repository.token`에서 `{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}`으로 참조.
- 로컬에서 `goreleaser check`는 기존부터 있던 `brews` 필드 deprecation 경고 때문에 exit 2가 나지만(이 작업 이전부터 존재하던 것, 무관), 실제 `goreleaser release`(`--snapshot --clean --skip=publish,sign`로 dry-run 검증)는 이 경고와 무관하게 정상 동작 확인.

### 실전 첫 릴리스에서 발견한 버그: 기본 `GITHUB_TOKEN`이 만든 태그는 다른 workflow를 못 깨움

처음 이 자동화로 Release PR(`chore(main): release 0.3.0`)을 merge했더니 release-please가 `v0.3.0` 태그까지는 정상 생성했지만, `release.yml`(goreleaser)이 **전혀 실행되지 않는** 문제 발생. 원인: GitHub Actions는 무한 루프 방지를 위해 기본 `GITHUB_TOKEN`으로 만든 push/태그는 다른 workflow의 트리거로 인정하지 않음 — `release-please-action`이 기본 토큰으로 태그를 만들었기 때문에 `on: push: tags:`가 반응하지 않았음.

- **수정**: `release-please-action`에 저장소 Actions secret `RELEASE_PLEASE_TOKEN`(fine-grained PAT, `ai-prompt-log` 자체에 대해 `Contents: Read and write` + `Pull requests: Read and write`)을 `token:` 입력으로 전달 — PAT으로 만든 태그 push는 "진짜 사용자 push"로 취급돼 정상적으로 `release.yml`을 발동시킴.
- `release.yml`에 `workflow_dispatch:` 트리거도 추가 — 이번처럼 태그가 이미 만들어졌는데 놓친 경우, `main`의 HEAD가 여전히 그 태그를 가리킬 때 `gh workflow run release.yml --ref main`으로 수동 복구 가능.
- 저장소 Actions 설정 `can_approve_pull_request_reviews`(= "Allow GitHub Actions to create and approve pull requests")도 기본값 `false`라 release-please의 첫 실행이 "GitHub Actions is not permitted to create or approve pull requests"로 실패했음 — 이것도 `true`로 켜야 함(워크플로우 YAML의 `permissions:` 블록과 별개의, 저장소 차원 게이트).

## `Ctrl+L` reload + 30초 변경 감지 (Python과 동일하게)

- `Model`에 `currentProjectIdx int`, `currentMTimes map[string]time.Time`, `changesPending bool`, `statusMessage string` 필드 추가. `setPromptsFrom()`이 project를 로드할 때마다 `snapshotMTimes()`(`source.ListSessionFiles` + `os.Stat`)로 mtime을 스냅샷.
- `handleKey`의 `ctrl+l` 케이스가 `reload()`를 호출: `hasChanges()`(스냅샷 vs 현재 mtime 비교)가 false면 다시 파싱하지 않고 `statusMessage = "변경 없음"`만 세팅, true면 `model.LoadProject()`로 다시 읽어 prompts pane/detail pane 및(aggregate 모드면) projects pane row까지 갱신.
- bubbletea에는 Textual의 `notify()` 같은 토스트가 없어 `checkChangesMsg` + `tea.Tick(30s, ...)`로 주기적 백그라운드 체크를 구현하고, footer 줄 앞에 `statusMessage`를 노란색(`styleYellow`)으로 붙여 알림을 표시 — Textual의 `border_subtitle`/토스트 역할을 footer 상태 줄로 대체. 이때도 **자동 reload는 하지 않음**(Python과 동일하게 `Ctrl+L`이 유일한 reload 트리거).
- `go test ./internal/tui/...`에 `TestReloadRealData` 추가: `backup.Run()`으로 실제 프로젝트 데이터를 격리된 임시 디렉터리에 복사해(운영 중인 `~/.claude/projects`는 건드리지 않음) no-op reload, 30초 주기 체크의 변경 감지(reload는 안 함), `Ctrl+L`로 실제 reload까지 전부 헤드리스로 검증. `go build`/`go vet`/`go test ./...`/`gofmt -l .` 전부 통과.

