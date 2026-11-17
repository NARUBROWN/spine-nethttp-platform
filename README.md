English | [Korean](README.ko.md)

# Spine net/http Platform

Run Spine applications on Go's standard `net/http` server. Spine core handles request bodies, DTO binding, and validation; the platform handles raw input, response output, and server lifetime. Spine is its only direct module dependency.

## Preview installation

Use this channel to try features under development and check compatibility. The published tag is [`v0.1.0-preview.3`](https://github.com/NARUBROWN/spine-nethttp-platform/tree/v0.1.0-preview.3). Its `go.mod` specifies Spine `v0.7.0-preview.4` and Go 1.25.5 or later. The next candidate on this branch is `v0.1.1-preview.1`; the release policy targets `v0.1.1`.

```sh
go get github.com/NARUBROWN/spine-nethttp-platform@v0.1.0-preview.3
```

Other channels: [RC](https://github.com/NARUBROWN/spine-nethttp-platform/tree/rc) | [Stable](https://github.com/NARUBROWN/spine-nethttp-platform/tree/main).

This platform implements `spine.http-adapter/v3`. To generate a net/http application with the CLI, pass `--platform nethttp` to `spine new`, then check the generated `go.mod` for selected core and platform versions. See [release operations](RELEASING.md) for publication procedures.

## Application setup

```go
import (
    "github.com/NARUBROWN/spine"
    nethttp "github.com/NARUBROWN/spine-nethttp-platform"
)

app := spine.New(nethttp.Option())
// Register controllers and routes with app.Constructor and app.Route.
```

`nethttp.New()` returns an application configured with the same platform. `nethttp.NewFactory()` returns `contract/http.Factory` and does not open a listener or start background goroutines during creation.

## Native middleware

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

`WithMiddleware` places earlier middleware on the outside. Factories run once per preparation after configuration validation. Nil functions and nil handler results cause preparation errors. Handle native routes directly in middleware and pass other requests to `next`.

The platform's admission gate and request tracking wrap the middleware. Shutdown also waits for native post-processing after `next` returns. Default dispatch does not use `ServeMux`, so it does not clean or redirect raw paths. Optional net/http WebSocket upgrades dispatch through GET routes.

## Serving and shutdown

Ordinary applications use Spine's `RunContext`. A manually prepared application can use `PreparedApplication.HTTPAdapter().Serve(listener)`. `Serve` owns the supplied listener and runs only once.

For external servers, attach the handler from `spine.NetHTTPHandler(prepared)`. Release application resources with `prepared.Close(ctx)`; the caller shuts down the external server itself.

After `StopAccepting`, new requests receive 503. `Shutdown` waits for in-flight requests and native post-processing. Cleanup continues if the calling context expires; wait again with a new context. Request contexts are cancelled when the native handler returns or the client disconnects. Spine's runtime closes long-lived WebSocket connections.

## TLS

Set `boot.HTTPOptions.TLS` to a `*tls.Config` containing loaded certificates. The example below uses `crypto/tls` and `github.com/NARUBROWN/spine/pkg/boot`.

```go
certificate, err := tls.LoadX509KeyPair("server.crt", "server.key")
if err != nil {
    return err
}
return app.RunContext(ctx, boot.Options{
    Address: ":8443",
    HTTP: &boot.HTTPOptions{
        TLS: &tls.Config{Certificates: []tls.Certificate{certificate}},
    },
})
```

A nil TLS configuration serves plaintext HTTP. Without an explicit address, TLS uses `:https` and plaintext uses `:http`. Prepare checks certificate configuration and capabilities without invoking certificate selection callbacks. Configuration and certificate bytes are copied; private key objects and callbacks are shared.

`Serve(listener)` uses the prepared TLS configuration. Pass `httpcontract.WithTLS(config)` as a second argument to override it; the last option wins. `httpcontract` is an import alias for `github.com/NARUBROWN/spine/contract/http`. `WithTLS(nil)` and nil options return errors. There is no additional public `ServeTLS`. When attaching only a handler, the external server configures and serves TLS. HTTP version selection and ALPN policy are outside this change.

## Options

Pass settings through `boot.HTTPOptions`.

| Option | Platform default when zero | Explicit value |
| --- | --- | --- |
| `ReadHeaderTimeout` | 5 seconds | Request header deadline |
| `ReadTimeout` | 30 seconds | Request read deadline, including headers and body |
| `WriteTimeout` | 30 seconds | Response write deadline |
| `IdleTimeout` | 120 seconds | Wait for the next keepalive request |
| `MaxHeaderBytes` | 1 MiB | net/http request line and header parsing limit |
| `MaxBodyBytes` | 32 MiB | Total body read limit; negative values disable it |
| `DisableRecover` | false | true returns `ErrUnsupportedOption` |

`net/http.Server` panic recovery cannot be disabled, so the platform does not advertise `DisableRecover` support. The core still handles controller panics in the Spine pipeline. For external servers, configure timeouts and header limits on that server. The platform handler still applies body read limits and request tracking.

`MultipartMemoryBytes` is the core's file memory budget. `MaxBodyBytes` limits file size; files exceeding the memory budget are stored temporarily on disk.

## Local verification

Run from the v3 core worktree.

```sh
python3 scripts/verify-http-platforms.py \
  --nethttp-root ../spine-nethttp-platform \
  --echo-root ../spine-echo-issue-28 \
  --output-dir /tmp/spine-http-platform-proof
```

The script runs platform tests and Echo comparison E2E tests with the race detector and vet on Go 1.25.5. It preserves repository `go.mod` and `go.sum` files. Real TCP E2E tests check DTO results, no reads before authentication, body limits, multipart file lifetime, shutdown draining, WebSocket, and runtime OpenAPI. TLS checks cover HTTPS serving, configuration overrides, certificate callbacks, failed trust verification, and cancellation on disconnect. HTTP/2 and performance are outside this verification scope.

## Releases and public contracts

This platform publishes its own versions and release channels. The Spine version in `go.mod` is the minimum dependency for its public contracts and is managed separately from the core release channel. CI and public installation checks verify the platform against this module's dependencies. Core releases verify combinations of a new core and existing platform tags.
