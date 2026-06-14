# runboxd

runboxd is an HTTP daemon that runs untrusted source code inside an OS-level
sandbox and returns the results: stdout, stderr, exit code, a status, and the
wall-clock duration.

It is a small, low-level code-execution backend: it runs code and reports what
happened, nothing more. It does not grade submissions, judge test cases, or
interpret intent — those are concerns for whatever calls it (a programming judge
or an agent runtime, for example).

Supported languages: Python, Node.js, C, and Java. Compiled languages run in a
two-stage compile-then-run jail.

## How it works

Everything goes through one interface (`internal/sandbox`):

```go
type Sandbox interface {
    Run(ctx context.Context, spec RunSpec) (RunResult, error)
    LangSpec(language, version string) (LangSpec, error)
    Close() error
}
```

The HTTP layer and worker pool depend only on this interface, so the isolation
backend can change without touching the rest. There are two backends:

| Backend                   | Isolation                                         | Notes                                                                  |
| ------------------------- | ------------------------------------------------- | ---------------------------------------------------------------------- |
| `DockerSandbox` (default) | containers                                        | portable; bounded by the Docker daemon's container create/destroy rate |
| `NsjailSandbox`           | namespaces, cgroups v2, rlimits, read-only rootfs | Linux-only; daemonless                                                 |

Select the backend at runtime with `SANDBOX_BACKEND={docker,nsjail}`.

Two things are pluggable by design:

- **Isolation backend** — anything satisfying the `Sandbox` interface drops in
  without touching the HTTP layer, worker pool, or API. Docker and nsjail are the
  two today; a future gVisor or microVM backend would be additive.
- **Languages** — these are configuration, not code. Each is a small
  `images/<lang>/image.yml` spec (base image, run/compile command, filename,
  resource limits) that `make gen-images` compiles into the registry the daemon
  reads. Adding a language or version means adding a spec and rebuilding — the
  same spec produces both the Docker image and the nsjail rootfs, so it works on
  either backend with no Go changes.

Incoming requests are handled by a bounded worker pool. When the pool and its
queue are full, further requests are rejected with HTTP 429 rather than queued
indefinitely.

## Isolation

The sandbox is the containment boundary. The nsjail backend applies, per run:

- A throwaway jail (`mode: ONCE`) with the process mapped to `nobody` via a user
  namespace, its own PID namespace, and a freshly mounted `/proc`.
- A read-only root filesystem (an exported per-language rootfs), the submitted
  code mounted read-only, and scratch space on `noexec`/`nosuid`/`nodev` tmpfs
  that is discarded when the jail exits.
- Resource limits via cgroups v2 (`memory.max`, `pids.max`, CPU bandwidth), with
  rlimits (`RLIMIT_AS`, `RLIMIT_NPROC`, `RLIMIT_FSIZE`, `RLIMIT_NOFILE`) as a
  fallback, plus a wall-clock time limit and bounded output capture.
- A private network namespace, so the code has no network access.

These properties are checked by an adversarial test suite (`make adversarial`,
`make adversarial-nsjail`) that runs against both backends.

Authentication is separate from isolation. `/execute` is gated by a static
bearer token (compared in constant time). The daemon is fail-closed: it will not
start without `AUTH_TOKEN` unless `AUTH_ALLOW_UNAUTHENTICATED=true` is set for
local development.

## API

| Method | Path       | Auth         | Purpose                                     |
| ------ | ---------- | ------------ | ------------------------------------------- |
| `GET`  | `/healthz` | open         | liveness                                    |
| `GET`  | `/readyz`  | open         | readiness (pings the backend)               |
| `GET`  | `/info`    | open         | supported languages and per-language limits |
| `POST` | `/execute` | bearer token | run code                                    |

Request:

```jsonc
// POST /execute  (Authorization: Bearer <token>)
{
  "language": "python",
  "version": "3.14", // optional; defaults per language
  "code": "import sys; print(sys.stdin.read().upper())",
  "stdin": "hello\n", // optional
  "workspace_files": [
    // optional extra files in the workspace
    { "path": "data.txt", "content": "..." },
  ],
  "timeout_seconds": 5, // optional; clamped to per-language limits
  "memory_bytes": 67108864, // optional; clamped to per-language limits
}
```

Response:

```jsonc
// 200 OK
{
  "stdout": "HELLO\n",
  "stderr": "",
  "exit_code": 0,
  "status": "ok",
  "duration_ms": 23,
}
```

`status` is one of `ok`, `runtime_error`, `timeout`, `oom`, `compile_error`, or
`internal_error`.

A failed execution (runtime error, timeout, OOM, compile error) is still an HTTP
200 with the details in the body. HTTP 4xx/5xx are used only for malformed
requests, auth failures, backpressure (429), and internal sandbox faults.

## Quick start (Docker backend)

Requires Docker and Go 1.26+.

```sh
# 1. Build the per-language execution images (runboxd-python, runboxd-c, ...).
make images

# 2. Build and run the daemon. Auth is fail-closed, so set a token or opt out:
make build
AUTH_ALLOW_UNAUTHENTICATED=true ./bin/runboxd
# (or: AUTH_TOKEN=dev-secret ./bin/runboxd)

# 3. In another shell:
curl -s localhost:8080/info | jq
curl -s -XPOST localhost:8080/execute \
  -H 'Content-Type: application/json' \
  -d '{"language":"python","code":"print(1+1)"}'
```

For the nsjail backend (Linux only): build the rootfs with `make rootfs`, then
run with `SANDBOX_BACKEND=nsjail ROOTFS_PATH=./_rootfs ./bin/runboxd`.

## Configuration

Configuration is via environment variables:

| Variable                     | Default                   | Description                                                                       |
| ---------------------------- | ------------------------- | --------------------------------------------------------------------------------- |
| `PORT`                       | `8080`                    | HTTP listen port                                                                  |
| `SANDBOX_BACKEND`            | `docker`                  | `docker` or `nsjail`                                                              |
| `WORKER_POOL_SIZE`           | `NumCPU`                  | max concurrent executions                                                         |
| `MAX_QUEUE_SIZE`             | `NumCPU`                  | requests queued before shedding 429s                                              |
| `AUTH_TOKEN`                 | —                         | bearer token for `/execute`; required unless the opt-out below is set             |
| `AUTH_ALLOW_UNAUTHENTICATED` | `false`                   | `true` runs `/execute` open (dev only; logs a warning)                            |
| `REGISTRY_PATH`              | `./language_registry.yml` | language manifest (generated by `make gen-images` from `images/<lang>/image.yml`) |
| `NSJAIL_PATH`                | —                         | path to the `nsjail` binary (nsjail backend)                                      |
| `ROOTFS_PATH`                | `./_rootfs`               | exported per-language rootfs tree (nsjail backend)                                |
| `CGROUP_V2_MOUNT`            | —                         | cgroup v2 mount point (nsjail backend)                                            |

Languages are defined in `images/<lang>/image.yml` (versions, base images, and
timeout/memory/PIDs/CPU limits). `make gen-images` compiles those into
`language_registry.yml`, which the daemon loads and exposes at `/info`.

## Testing

| Command                   | Covers                                                               |
| ------------------------- | -------------------------------------------------------------------- |
| `make test`               | unit tests                                                           |
| `make cover`              | coverage (add `GOFLAGS=-tags=integration` for the Docker `Run` path) |
| `make integration`        | end-to-end execution on the Docker backend                           |
| `make integration-nsjail` | end-to-end execution on the nsjail backend                           |
| `make adversarial`        | containment/escape suite on Docker                                   |
| `make adversarial-nsjail` | containment/escape suite on nsjail                                   |
| `make load`               | load sweep against a running server (vegeta)                         |
| `make lint`               | `go vet ./...`                                                       |

## Project layout

```
cmd/runboxd/        # the daemon: server wiring and graceful shutdown
cmd/runboxctl/      # build tooling: image generation, rootfs export, lockfile
internal/api/       # HTTP handlers, DTOs, worker pool, auth middleware
internal/sandbox/   # the Sandbox interface and the Docker and nsjail backends
internal/config/    # environment-sourced configuration
images/             # per-language image specs (source for the registry)
```

## Future work

- Make compile-time limits configurable per request, for languages with heavy
  builds.
- A compile-once, run-many endpoint (run one program against several inputs
  without recompiling).
- A per-language capacity table, to size deployments by language mix.
- A simple web front-end for trying code interactively.
