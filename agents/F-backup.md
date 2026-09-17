# Agent F — durable backup + backup viewer (계약서)

## Intent

Claude Code 세션 로그는 오직 `~/.claude/projects/<encoded-cwd>/`에만 존재한다. 사용자가
프로젝트 디렉터리를 지우면 그 프로젝트에서 무엇을 했는지 되짚어볼 방법이 없어진다.
어느 한 프로젝트의 생명주기에도 종속되지 않는, 사람이 직접 열어볼 수 있는(jsonl 그대로)
내구성 있는 백업 저장소와, 그 백업을 나중에 다시 들여다보는 뷰어를 추가한다.

범위 밖:
- 백업 스케줄링/자동화(cron 등) — 이번엔 수동 `apl --backup` 실행만 다룬다.
- 백업 파일 압축/암호화 — jsonl을 그대로(사람이 읽을 수 있게) 복사한다.
- 백업본 삭제/정리(prune) 기능 — 백업은 append-only, 아무것도 지우지 않는다.
- `cmd/`, `internal/`, `go.mod`, `go.sum`(Go 포팅) — 별도 세션이 진행 중이라 이번 범위 밖.
- 기존 `apl --all`(live, no-copy 집계 뷰) 동작 변경 — 이번 기능은 추가(3번째 모드)일 뿐,
  기존 direct/aggregate 모드는 그대로 둔다.

## Input

- `docs/data-model.md` — Prompt/Project 파싱 규칙 (변경 없이 그대로 재사용)
- `poc/apl/source.py`, `poc/apl/model.py`, `poc/apl/tui.py`, `poc/apl/cli.py` — 기존 구현
- `git show c7200c4:poc/apl/sync.py` — 폐기된 `apl sync`의 mtime+size 기반 증분 복사 로직
  (참고용 레퍼런스 구현. 이번 백업은 "지우지 않는다"는 점만 다르고 복사 판단 로직은 동일)
- 이 머신의 실제 `~/.claude/projects` (16개 프로젝트, 실측 검증에 사용)

## Result (계약)

### CLI 표면

- `apl --backup [--depth N] [--backup-dir PATH]`
  - `--depth` 생략 시: 현재 프로젝트 하나만 백업(`source.find_direct_project_dir()`와 동일한
    "가장 가까운 조상" 판정 재사용).
  - `--depth N`: cwd에서 N단계 상위 디렉터리를 root로 잡고, `~/.claude/projects`의 모든
    프로젝트 중 **jsonl 레코드의 실제 `cwd` 필드**가 그 root와 같거나 하위인 프로젝트를
    전부 백업 대상으로 삼는다(인코딩된 디렉터리명을 역디코딩하지 않음 — 손실 인코딩이므로).
  - `--backup-dir PATH`: 기본값 `~/ai-prompt-log.backup/`(홈 디렉터리, 특정 프로젝트에
    종속되지 않음, git-ignore 대상 아님).
  - 기존 mtime+size 증분 복사 로직 재사용: 새 파일은 추가, 바뀐 파일(mtime/size 다름)은
    갱신, 그 외엔 unchanged로 건너뜀. **삭제는 절대 하지 않는다**(옛 sync.py와의 유일한
    의도적 차이).
  - 완료 후 `프로젝트 N개, 복사 X / 갱신 Y / 변경없음 Z -> <backup-dir>` 형태로 요약 출력
    (옛 sync.py의 `print(...)` 스타일 계승).
- `apl --view-backup [--backup-dir PATH]`
  - 기존 aggregate(3-pane: Projects | Prompts | Detail) TUI 코드 경로를 그대로 재사용하되,
    루트 디렉터리만 `~/.claude/projects` 대신 백업 디렉터리로 지정.
  - `source.detect_mode()`에 `root_override` 파라미터를 추가해 구현(모델/파싱 로직 변경 없음).

### 파일

- `agents/F-backup.md`(본 파일)
- `poc/apl/backup.py` — depth→root 해석, 실제 cwd 기반 프로젝트 매칭, 증분 복사, 통계
- `poc/apl/source.py` — `detect_mode()`에 `root_override` 파라미터 추가
- `poc/apl/cli.py` — `--backup`/`--depth`/`--backup-dir`/`--view-backup` 플래그, `--help` 텍스트 갱신
- `poc/apl/tui.py` — `AplApp`에 `root_override` 전달 경로 추가(가능한 한 최소 변경)
- `plan.md` — 새 서브섹션(§2-4 또는 §7 액션아이템)으로 백업 기능 설계/결정 기록
- `README.md` — 사용법 섹션에 `--backup`/`--view-backup` 플래그 추가
- `docs/data-model.md` — 백업 디렉터리가 `~/.claude/projects/<encoded-cwd>/`와 동일 레이아웃을
  미러링하므로 기존 파싱 규칙이 그대로 적용된다는 점만 한 줄 추가(파싱 규칙 자체는 불변)

### 수용 기준 (실데이터로 검증)

1. `--depth` 없이 `apl --backup` 실행 시 현재 프로젝트 하나만 백업됨
2. `--depth 1`(또는 그 이상) 실행 시 cwd 기준 실제 `cwd` 조상 관계로 형제 프로젝트들이
   정확히 포함됨(이 머신의 실제 16개 프로젝트로 검증)
3. `apl --backup` 재실행(idempotent) 시 두 번째 실행에서 변경 없는 파일은 unchanged로
   보고되고, 실제로 바뀐 파일만 updated로 보고됨
4. `apl --view-backup`이 백업된 프로젝트를 정확히 보여줌(헤드리스 Textual `run_test()`
   파일럿 — row 개수, 실시간 pane 갱신 등)

## 상태: 완료 — 실행 검증 로그

모두 실제 데이터(이 머신의 실제 `~/.claude/projects`, 실제 프로젝트 16개)로 검증함. mock 없음(단, CLI 인자 파싱 자체가 올바른 인자로 `AplApp`을 호출하는지만 `unittest.mock`으로 별도 확인 — 데이터는 여전히 실제).

1. **`select_projects()` 단위 검증 (실제 `~/.claude/projects`, cwd=`/data01/cheoljoo.lee/code/ai-prompt-log`)**
   - `depth=None` → 현재 프로젝트 1개만(`-data01-cheoljoo-lee-code-ai-prompt-log`)
   - `depth=1` → root=`/data01/cheoljoo.lee/code`, 실제 프로젝트 **16개 전부** 매칭. 각 프로젝트의 실제 `cwd` 필드를 출력해 전수 확인(`ai-prompt-log`, `llm_wiki`, `hermes`, `herdr`, `ccr`, `pvs_crawler` 등 — 인코딩된 디렉터리명이 아니라 jsonl에 기록된 실제 `cwd` 값으로 판정했음을 확인). 흥미로운 엣지 케이스: `-data01-cheoljoo-lee-code` 프로젝트는 실제 `cwd`가 root 자신(`/data01/cheoljoo.lee/code`)이라 "root 자신도 포함" 규칙대로 정상 포함됨.
   - `depth=2` → root=`/data01/cheoljoo.lee`도 동일하게 16개(이 머신엔 `/data01/cheoljoo.lee/code` 바깥에 기록된 프로젝트가 없어서 결과가 같음 — depth 값 자체가 올바르게 다른 root로 해석되는 것은 `resolve_depth_root()` 출력으로 별도 확인)
2. **`copy_project()` 단위 검증(격리된 임시 소스/목적지 디렉터리, mtime 조작 포함)**
   - 최초 복사: `(copied, updated, unchanged) == (1, 0, 0)`
   - 변경 없이 재실행: `(0, 0, 1)` — unchanged로 정확히 보고
   - 파일 내용을 바꾸고(mtime도 자연히 바뀜) 재실행: `(0, 1, 0)` — updated로 잡히고 목적지 파일이 실제로 새 내용으로 갱신됨
   - 새 세션 파일 추가 후 재실행: `(1, 0, 1)` — 새 파일만 copied, 기존 파일은 unchanged
   - **소스에서 파일을 삭제한 뒤 재실행**: `(0, 0, 1)`이고 목적지엔 여전히 그 파일이 남아있음 — "백업은 절대 삭제하지 않는다"는 요구사항을 직접 확인
3. **실제 CLI end-to-end, 기본 백업 위치(`~/ai-prompt-log.backup/`)**
   - `apl --backup --depth 1` 최초 실행: `apl backup: 16 project(s), 26 copied, 0 updated, 0 unchanged -> /data01/cheoljoo.lee/ai-prompt-log.backup`
   - 동일 커맨드 즉시 재실행(idempotency): `apl backup: 16 project(s), 0 copied, 0 updated, 26 unchanged -> /data01/cheoljoo.lee/ai-prompt-log.backup`
   - 백업 디렉터리 아래 실제 프로젝트 디렉터리 16개 생성 확인, 총 90M(원본 `~/.claude/projects` 104M — 세션 파일만 복사하고 그 외 메타 파일은 없어서 약간 작음)
   - `git status --porcelain`으로 이 백업 디렉터리가 저장소 바깥(홈 디렉터리)에 있어 repo에 전혀 나타나지 않음을 확인
4. **`apl --view-backup` 헤드리스 Textual `run_test()` 파일럿 (실제 백업 데이터, 기본 위치)**
   - `AplApp(root_override=backup.DEFAULT_BACKUP_DIR)` → `screen.mode == "aggregate"`, Projects pane row_count == 16 확인
   - `g`/`G` 커서 이동 정상(맨 위/맨 아래로 이동, crash 없음)
   - 별도 커스텀 `--backup-dir`(scratchpad 임시 경로)로 만든 백업에 대해서도 동일 파일럿: Projects 16개, `j`로 프로젝트 전환 시 Prompts pane row_count가 실시간 갱신(7 → 18, 실제 두 프로젝트의 실제 prompt 개수 차이), `l`로 포커스가 `projects-table → prompts-table → detail-pane` 순으로 정확히 이동함을 확인
5. **회귀 확인**: 이번 변경 이후에도 `apl`(direct 모드)과 `apl --all`(기존 aggregate, `~/.claude/projects` 직접 읽기)이 기존과 동일하게 동작함을 헤드리스로 재확인 — `apl --all`은 여전히 16개 프로젝트를 `~/.claude/projects`에서 직접 읽고, `apl`은 여전히 direct 모드로 이 프로젝트의 로그 디렉터리를 정확히 가리킴
6. **CLI 인자 검증**
   - `apl --depth 1`(단독) → `apl: error: --depth only makes sense with --backup`, exit 2
   - `apl --backup-dir /tmp/x`(단독) → `apl: error: --backup-dir only makes sense with --backup or --view-backup`, exit 2
   - `apl --all --backup`(동시 지정) → argparse 상호배타 그룹으로 인해 `apl: error: argument --backup: not allowed with argument -a/--all`, exit 2
   - `apl --view-backup [--backup-dir PATH]` 인자 조합별로 `AplApp(aggregate=False, root_override=...)`가 정확한 값으로 호출됨을 `unittest.mock`으로 확인(TUI 이벤트 루프는 시작하지 않고 호출 인자만 검증 — 실데이터 검증은 위 3, 4번에서 이미 완료했으므로 여기서는 배선(wiring)만 확인)
   - `apl --help` 출력에 `--backup`/`--view-backup`/`--depth`/`--backup-dir` 옵션 설명이 모두 정상 노출됨을 확인

### 알려진 한계
- `--depth`로 조상 디렉터리를 판단할 때 symlink 등으로 실제 물리 경로가 다르면 어긋날 수 있음(§6 리스크의 기존 "project 매칭" 제약과 동일선상, 새로 생긴 문제 아님)
- 백업 디렉터리 자체의 보존(디스크 공간, 오래된 백업 정리)은 이번 범위 밖 — append-only이므로 계속 쌓인다(Intent에 명시한 대로 prune 기능은 다루지 않음)
