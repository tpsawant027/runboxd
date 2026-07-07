# Authoring languages and tests

Each language runboxd supports is defined as **configuration**. A language lives in
`images/<lang>/` as two hand-authored YAML files:

- **`image.yml`** - how to build and run the language: base image, the command
  that compiles/runs submitted code, resource limits. **Required.**
- **`tests.yml`** - per-language conformance and smoke tests. **Optional**, but
  every shipped language has one.

You can add a language or a version without touching Go. Both files are
**strictly decoded** (`KnownFields(true)`): an unknown or misspelled key is a hard
error at load time. The Go structs are the schema -
[`internal/imagespec/imagespec.go`](../internal/imagespec/imagespec.go) for `image.yml`, [`internal/langtest/fixture.go`](../internal/langtest/fixture.go)
for `tests.yml` - and this document tracks them.

> **Generated, do not hand-edit:** `language_registry.yml` and the image lockfile
> are produced from these files by `runboxctl images gen-images` /
> `runboxctl images gen-lock` (`make gen-images` / `make gen-lock`). The source
> of truth is always `images/<lang>/`;
> never edit the registry directly.

---

## `image.yml`

### Example - interpreted (Python)

```yaml
name: python # language id (must match the directory name)
type: interpreted # no compile step
filename: main.py # submitted source is written here, in /sandbox
default_version: "3.14" # used when a request omits a version
exec_cmd: python # the language's primary binary (metadata; launch is run_cmd)
limits: # per-run sandbox caps (see "Limits" below)
  min_memory_mib: 64
  max_memory_mib: 128
  min_timeout_seconds: 1
  max_timeout_seconds: 10
  max_pids: 64
versions: # map of version string -> how to run it
  "3.14":
    base_image: python:3.14-slim
    run_cmd: ["python", "/sandbox/main.py"]
  "3.13":
    base_image: python:3.13-slim
    run_cmd: ["python", "/sandbox/main.py"]
```

### Example - compiled (Go)

```yaml
name: go
type: compiled # adds a separate compile step (build_cmd + compile_limits)
filename: main.go
default_version: "1.26"
exec_cmd: go
env: # environment baked into the image at build time
  GOPATH: "/go"
  GOCACHE: "/opt/gocache"
  GOTMPDIR: "/sandbox/.gotmp"
  PATH: "/go:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
limits: # the RUN jail (the compiled binary)
  min_memory_mib: 64
  max_memory_mib: 256
  min_timeout_seconds: 1
  max_timeout_seconds: 10
  max_pids: 64
  workspace_size_mib: 64
compile_limits: # the COMPILE jail (go build) - compiled langs only
  memory_mib: 512
  timeout_seconds: 10
  max_pids: 64
  max_cpus: 1
  workspace_size_mib: 64
setup: # shell hooks run at IMAGE-BUILD time (baked in, not per-run)
  - "GOCACHE=/opt/gocache go build std" # prewarm the stdlib build cache
versions:
  "1.26":
    base_image: golang:1.26-alpine
    build_cmd:
      ["go", "build", "-o", "/build/main", "-ldflags=-s -w", "/sandbox/main.go"]
    run_cmd: ["/build/main"]
```

### Top-level fields

| Field             | Type                | Notes                                                                                                                                           |
| ----------------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`            | string, required    | Language id; match the directory name.                                                                                                          |
| `type`            | string, required    | `interpreted` or `compiled`. `compiled` requires a `build_cmd` per version and uses `compile_limits`.                                           |
| `filename`        | string, required    | Filename the submitted source is written to inside `/sandbox`.                                                                                  |
| `default_version` | string, required    | Must be a key in `versions`. Used when a request omits a version.                                                                               |
| `exec_cmd`        | string, required    | The language's main binary. Recorded in the generated registry as metadata; the actual launch command is `run_cmd` (and `build_cmd`), not this. |
| `env`             | map\[string\]string | Environment baked into the image **at build time**.                                                                                             |
| `setup`           | []string            | Shell hooks run **at image-build time** (e.g. prewarming a cache), not per request.                                                             |
| `limits`          | object              | Per-run resource caps. See below.                                                                                                               |
| `compile_limits`  | object              | Compile-step caps (compiled languages only). See below.                                                                                         |
| `versions`        | map                 | version string -> `{base_image, build_cmd, run_cmd}`. At least one required.                                                                    |

**`build_cmd` and `run_cmd` are argv arrays, not shell strings.** Write
`["go", "build", "-o", "/build/main", "/sandbox/main.go"]`, not
`"go build -o /build/main /sandbox/main.go"`. There is no shell to split words or
expand globs/variables - each element is one argument passed directly to `execve`.
`build_cmd` is omitted for interpreted languages. By convention the run jail mounts
`/sandbox` (source, read-only) and the compile jail also has a writable `/build`
for compiler output.

### Limits

Two independent tiers, mapping to two jails:

- **`limits`** - the **run** jail (the interpreter, or the compiled binary).
- **`compile_limits`** - the **compile** jail (`build_cmd`). Compiled languages
  only; interpreted languages have no compile phase, so it's ignored.

| `limits` field                                | Unit                                | `compile_limits` field | Unit            |
| --------------------------------------------- | ----------------------------------- | ---------------------- | --------------- |
| `min_memory_mib` / `max_memory_mib`           | MiB                                 | `memory_mib`           | MiB             |
| `min_timeout_seconds` / `max_timeout_seconds` | seconds                             | `timeout_seconds`      | seconds         |
| `max_pids`                                    | count                               | `max_pids`             | count           |
| `max_cpus`                                    | fractional CPUs (float, e.g. `0.5`) | `max_cpus`             | fractional CPUs |
| `workspace_size_mib` (`/sandbox` tmpfs)       | MiB                                 | `workspace_size_mib`   | MiB             |
| `tmp_size_mib` (`/tmp` tmpfs)                 | MiB                                 | `tmp_size_mib`         | MiB             |

Memory and timeout in the run tier are **ranges**: a per-request value is clamped
to `[min, max]`. The compile tier is a single budget (no min/max).

**Unset means "use the default", not "unlimited."** Any field you omit, or set to
`0`, resolves to a conservative built-in default; there is no way to express
"no limit." So an omitted limit is a _safe_ default, and you only need to set a
field to move it off the default. You can set values **above** the defaults
(there's no `NumCPU` ceiling - operators are trusted). Validation rejects only
nonsensical inputs: `max_cpus` ≤ 0 or non-finite, `max_pids` < 1, negative
timeouts, and `min` > `max`.

The default values live in one `const` block, [`internal/sandbox/limits.go`](../internal/sandbox/limits.go)
(shared by both backends). At the time of writing:

| Field                 | Default                        |
| --------------------- | ------------------------------ |
| `max_memory_mib`      | 256                            |
| `min_memory_mib`      | 64 (clamped to ≤ resolved max) |
| `max_timeout_seconds` | 10                             |
| `min_timeout_seconds` | 1 (clamped to ≤ resolved max)  |
| `max_pids`            | 100                            |
| `max_cpus`            | 0.5                            |
| `workspace_size_mib`  | 10                             |
| `tmp_size_mib`        | 5                              |

`compile_limits` fills unset fields from the same defaults (`memory_mib` -> the
256 MiB max-memory default). Treat `limits.go`'s `const` block as authoritative if
these numbers and the doc ever disagree.

> **Backend note:** the separate compile budget is enforced by the **nsjail**
> backend (a distinct compile jail with its own cgroup). Under the **docker**
> backend the `compile_limits` field is currently inert.

---

## `tests.yml`

Per-language tests, co-located at `images/<lang>/tests.yml`. Also strictly decoded.
They run two ways:

- `go test -tags conformance ./internal/langtest/` (and the `make conformance` /
  `make conformance-nsjail` targets) - drives a real backend.
- The loader/validator itself is unit-tested in the default suite, so a malformed
  `tests.yml` is caught fast without a sandbox.

### Example (Python)

```yaml
language: python
# version: "3.13"          # optional; omitted = the language's default_version

conformance: # closed, capability-keyed; the HARNESS owns the expected status
  oom:
    source: |
      import os
      os.urandom(2 << 30)  # allocate far above the cap
    memory_bytes: 33554432 # 32 MiB
  timeout:
    source: |
      import time
      time.sleep(60)
    timeout_ms: 1000
  fs_escape:
    source: |
      open("/oops.txt", "w")   # write outside the workspace
    want_stderr_contains: "Read-only file system"

smoke: # a list; fully author-controlled, full matcher set
  - name: hello
    source: |
      print("hi")
    want_stdout: "hi\n"
  - name: reads_workspace_file
    source: |
      print(open("data.txt").read())
    files:
      - name: data.txt
        content: "hello\n"
    want_stdout_contains: "hello"
  - name: deliberate_error_surfaces
    source: |
      raise ValueError("boom")
    want_status: runtime_error
    want_stderr_contains: "boom"
```

### Two layers

**`conformance`** is a **closed map keyed by capability**. The only allowed keys are
`oom`, `timeout`, `fs_escape`, `compile_error`. You do **not** write the expected
status - the harness owns it (`oom -> OOM`, `timeout -> Timeout`, `fs_escape ->
RuntimeError`, `compile_error -> CompileError`). You supply the `source` that should
trigger the capability, plus the trigger's parameter:

| Key             | Required (besides `source`)        | Proves                                          |
| --------------- | ---------------------------------- | ----------------------------------------------- |
| `oom`           | `memory_bytes` (> 0)               | the memory cap is enforced (kill, not graceful) |
| `timeout`       | `timeout_ms` (> 0)                 | the wall-clock cap fires                        |
| `fs_escape`     | `want_stderr_contains` (non-empty) | the root filesystem is read-only                |
| `compile_error` | - (source alone)                   | a bad compile is reported as `compile_error`    |

`source` is required for every key. `want_stderr_contains` is optional on the keys
that don't require it. Note the **units**: `memory_bytes` is **bytes** and
`timeout_ms` is **milliseconds** here - unlike `image.yml`, which uses MiB and
seconds.

**`smoke`** is a list of fully author-controlled cases with the complete matcher set:

| Field                         | Notes                                                                         |
| ----------------------------- | ----------------------------------------------------------------------------- |
| `name`                        | required; unique label                                                        |
| `source`                      | required                                                                      |
| `stdin`                       | piped to the program                                                          |
| `files`                       | extra workspace files: `[{name, content}]`                                    |
| `timeout_ms` / `memory_bytes` | per-case overrides (bytes / ms)                                               |
| `want_status`                 | default `ok`; one of `ok`, `runtime_error`, `timeout`, `oom`, `compile_error` |
| `want_exit_code`              | exact exit code (omit to skip)                                                |
| `want_stdout`                 | exact match                                                                   |
| `want_stdout_contains`        | substring match                                                               |
| `want_stderr_contains`        | substring match                                                               |

### OOM gate

The conformance `oom` case asserts a real OOM **kill**, which needs a backend that
can enforce the memory cap (Docker with swap accounting, or nsjail cgroups). On a
host that can't (e.g. Docker without swap limits), the `oom` case is **skipped**,
not failed.
