# Agent A — 데이터 모델 스펙 조사 (계약서)

## Intent
`~/.claude/projects/**/*.jsonl`의 실제 구조를 조사해 Project/Prompt 정규화 규칙을 확정한다.

## Input
- 로컬 `~/.claude/projects/` 아래 실제 세션 jsonl 파일들 (여러 프로젝트, 대용량 파일 포함)

## Result (계약)
- `docs/data-model.md`에 확정된 파싱 규칙 문서화
- 수용 기준: 아래 항목이 모두 실측 근거와 함께 문서에 포함되어야 완료로 간주
  1. 경로 인코딩 규칙 (`cwd` → 디렉터리명)
  2. prompt 경계 판정 규칙
  3. assistant 응답 블록 종류와 처리 방법
  4. `/clear`, `/compact` 시 원본 보존 여부
  5. 정렬 기준(시간순 vs 줄 순서)

## 상태: 완료
조사는 plan.md 작성 과정(§2, §2-1, §2-2)에서 실측으로 이미 수행됨. 본 문서는 그 결과를 `docs/data-model.md`로 정리한 것.
