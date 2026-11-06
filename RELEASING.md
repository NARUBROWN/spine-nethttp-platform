# net/http 플랫폼 릴리스 운영

대상 버전은 `v0.1.0`이며 코어 `v0.7.0`과 같은 채널로 발행합니다. 패키지 버전 번호는 코어와 독립적입니다. 현재 후보 태그와 공개 설치 조합은 아직 발행하거나 검증하지 않았습니다.

| 채널 | 플랫폼 후보 | 코어 의존성 | 브랜치 |
| --- | --- | --- | --- |
| Preview | v0.1.0-preview.1 | v0.7.0-preview.3 | preview |
| RC | v0.1.0-rc.1 | v0.7.0-rc.1 | rc |
| 정식 | v0.1.0 | v0.7.0 | main |

정확한 조합은 `.github/release-policy.json`에 고정합니다. 소스 CI는 `ci_core`의 전체 SHA와 임시 workspace를 사용합니다. 제품 `go.mod`에 로컬 replace를 넣거나 아직 없는 태그의 체크섬을 만들지 않습니다. 공개 발행에서는 `GOWORK=off`로 실제 코어 태그를 사용합니다.

## 저장소와 승인 환경

초기 기준에는 릴리스 제어 파일과 최소 패키지만 두고 HTTP 구현을 preview 대상 PR로 검토합니다. `preview`, `rc`, `main`은 삭제와 강제 갱신을 막으며 PR마다 `Verified Source / <대상 브랜치>`와 `Branch Flow / <대상 브랜치>`를 요구합니다. RC와 정식 승격에는 merge commit을 사용합니다. `v*` 태그는 수정하거나 삭제하지 않습니다.

`release-preview`와 `release-rc`는 각각 같은 이름의 채널 브랜치만 허용합니다. `release-stable`은 main과 지정 리뷰어 NARUBROWN의 승인을 요구하며 관리자 우회를 허용하지 않습니다. 초기 설정 후 다음 명령으로 원격 규칙과 승인 환경을 다시 확인합니다.

```sh
python3 scripts/release/configure_branches.py
```

## 발행 순서

1. 코어의 해당 채널 후보 태그를 먼저 발행합니다.
2. `python3 scripts/release/pin_channel.py --channel preview`로 변경을 검토합니다. 승인한 경우 `--apply --tidy`를 추가해 실제 공개 의존성과 체크섬을 반영합니다.
3. 변경을 preview에 합치고 전체 소스 CI를 확인합니다. Actions의 **Publish Preview**에서 새 태그와 preview HEAD 전체 SHA를 지정합니다.
4. 발행 workflow는 출처와 정책, 실제 공개 모듈, race/vet을 검사한 뒤 태그를 만듭니다. 태그 생성 후에도 새 소비자 캐시에서 설치와 테스트를 확인합니다. 실패와 skip은 통과로 처리하지 않습니다.
5. Preview와 RC는 `release-manifest-<태그>` artifact를 남깁니다. 정식만 GitHub Release와 manifest를 게시합니다.

코어의 전체 조합은 net/http를 포함한 여섯 모듈의 태그, manifest와 공개 검증이 있어야 완료됩니다. 이 저장소의 PR이나 태그 하나로 전체 조합 완료를 판단하지 않습니다.

Preview 완료 후 72시간, RC 완료 후 24시간을 관찰하고 정식 발행 간격은 168시간입니다. 긴급 관찰 기간 단축 사유는 20자 이상 기록하며 CI, 공개 검증과 정식 승인 자체는 생략하지 않습니다. 새 코드가 있으면 새 Preview로 검증합니다.

## 검증

```sh
python3 -m unittest discover -s test/release -p 'test_*.py'
node --test scripts/release/branch_flow.test.cjs
python3 scripts/release/release.py validate-policy
python3 scripts/release/release.py check-module --channel preview
actionlint -shellcheck= -pyflakes=
```

CI는 Linux, macOS, Windows의 테스트와 Linux race 검사를 실행합니다. 공개 코어 태그가 없는 동안은 고정한 코어 소스로 검사하며, 이 결과를 공개 설치 성공으로 대신하지 않습니다.

태그 생성 직후의 선택적인 Discord 알림은 공통 Action을 전체 SHA로 고정합니다. `DISCORD_WEBHOOK_PREVIEW`, `DISCORD_WEBHOOK_RC`, `DISCORD_WEBHOOK_RELEASE`는 별도 repository secret입니다. 값이 없거나 전송이 실패해도 태그 이후 발행 검증은 계속하며, 알림이 실제 배포 완료를 뜻하지 않습니다.
