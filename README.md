# Spine net/http 플랫폼

Spine 앱을 Go 표준 `net/http` 서버에서 실행합니다. 요청 본문, DTO 바인딩과 검증은 Spine 코어가 처리하며 플랫폼은 원시 입력, 응답 출력과 서버 수명을 담당합니다. 직접 의존하는 모듈은 Spine 하나입니다.

현재는 HTTP 계약 `spine.http-adapter/v3`를 구현한 로컬 개발 모듈입니다. 공개된 v2 코어와 함께 사용할 수 없습니다. 아래 검증 스크립트는 v3 코어와 플랫폼 소스를 임시 main-module 설정으로 연결하며 공개 모듈 설치는 별도 검증 대상입니다.

## 앱 구성

```go
import (
    "github.com/NARUBROWN/spine"
    nethttp "github.com/NARUBROWN/spine-nethttp-platform"
)

app := spine.New(nethttp.Option())
// app.Constructor와 app.Route로 컨트롤러와 경로를 등록합니다.
```

`nethttp.New()`는 같은 플랫폼을 연결한 앱을 반환합니다. `nethttp.NewFactory()`는 `contract/http.Factory`를 반환하며 생성 시 리스너나 실행 고루틴을 만들지 않습니다.

## Native 미들웨어

```go
factory := nethttp.NewFactory(nethttp.WithMiddleware(
    func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("X-Service", "example")
            next.ServeHTTP(w, r)
        })
    },
))
app := spine.New(spine.WithHTTPAdapter(factory))
```

`WithMiddleware`는 먼저 전달한 미들웨어를 바깥에 배치합니다. 모든 생성 함수는 설정 검증 후 준비마다 한 번 호출합니다. nil 함수와 nil handler 결과는 준비 오류입니다. native 경로를 처리하려면 미들웨어에서 해당 요청에 응답하고, 나머지 요청은 `next`에 전달합니다.

플랫폼의 수락 차단과 요청 추적은 미들웨어 바깥에서 시작합니다. `next` 반환 후의 native 후처리도 종료 대기에 포함합니다. 플랫폼의 기본 분기는 `ServeMux`를 사용하지 않으므로 원시 경로를 정리하거나 리다이렉트하지 않습니다. 선택적인 net/http WebSocket 업그레이드는 GET 경로로 분기합니다.

## 실행과 종료

일반 앱은 Spine의 `RunContext`로 실행합니다. 직접 준비한 앱은 `PreparedApplication.HTTPAdapter().Serve(listener)`로 실행할 수 있습니다. `Serve`는 전달받은 리스너를 소유하며 한 번만 실행합니다.

외부 서버에는 `spine.NetHTTPHandler(prepared)`로 얻은 handler를 연결합니다. 앱 자원은 `prepared.Close(ctx)`, 외부 서버 자체는 호출자가 종료합니다.

`StopAccepting` 이후 새 요청에는 503을 반환합니다. `Shutdown`은 진행 요청과 native 후처리를 기다립니다. 호출 context가 만료돼도 정리는 계속되며 새 context로 다시 기다릴 수 있습니다. 요청 context는 native handler가 반환하거나 클라이언트가 연결을 끊으면 취소됩니다. 장기 WebSocket 연결은 Spine 런타임이 종료합니다.

## 옵션

설정은 `boot.HTTPOptions`에서 전달합니다.

| 옵션 | 0일 때 플랫폼 기본값 | 명시 값 |
| --- | --- | --- |
| `ReadHeaderTimeout` | 5초 | 요청 헤더 수신 기한 |
| `ReadTimeout` | 30초 | 헤더와 본문을 포함한 요청 읽기 기한 |
| `WriteTimeout` | 30초 | 응답 쓰기 기한 |
| `IdleTimeout` | 120초 | 다음 keepalive 요청 대기 기한 |
| `MaxHeaderBytes` | 1 MiB | net/http의 요청 줄과 헤더 파싱 한도 |
| `MaxBodyBytes` | 32 MiB | 읽는 전체 본문 한도, 음수는 해제 |
| `DisableRecover` | false | true는 `ErrUnsupportedOption` |

`net/http.Server` 자체의 패닉 복구는 끌 수 없어 `DisableRecover` 지원을 선언하지 않습니다. Spine 파이프라인의 컨트롤러 패닉 처리는 코어에 남습니다. 외부 서버로 실행할 때 timeout과 헤더 크기는 외부 서버에 설정해야 하며 본문 읽기 제한과 요청 추적은 플랫폼 handler에서도 적용됩니다.

`MultipartMemoryBytes`는 코어의 파일 메모리 예산입니다. 파일 크기 제한은 `MaxBodyBytes`가 담당하며, 예산을 넘긴 파일은 임시 디스크에 저장합니다.

## 로컬 검증

v3 코어 작업트리에서 실행합니다.

```sh
python3 scripts/verify-http-platforms.py \
  --nethttp-root ../spine-nethttp-platform \
  --echo-root ../spine-echo-issue-28 \
  --output-dir /tmp/spine-http-platform-proof
```

스크립트는 Go 1.25.5에서 플랫폼 테스트와 Echo 비교 E2E에 race 검사와 vet을 실행합니다. 저장소의 `go.mod`, `go.sum`은 변경하지 않습니다. E2E는 실제 TCP 서버에서 DTO 결과, 인증 전 무읽기, 본문 제한, multipart 파일 수명, 종료 대기, WebSocket과 runtime OpenAPI를 검사합니다. TLS, HTTP/2와 성능은 이 검증 범위에 포함하지 않습니다.
