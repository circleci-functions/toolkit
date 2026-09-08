# CircleCI Functions SDK

The SDK lets a CircleCI function perform privileged job operations — saving and restoring caches, storing artifacts, and more — without holding its own credentials.

## Usage

Call `sdk.Run` in `main`. It handles signal shutdown and passes a ready-to-use `*Client` to your function.

```go
package main

import (
    "context"
    "flag"
    "fmt"

    "github.com/circleci-functions/toolkit/sdk"
)

var checksum = flag.String("checksum", "", "value to partition the cache key by")

func main() {
    flag.Parse()
    sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
    // A cache key is built at runtime. {{ checksum "go.sum" }} is expanded by the
    // config compiler, so a template string hardcoded here would be saved
    // verbatim, braces and all — take it as a flag instead.
    key := fmt.Sprintf("go-modules-%s-%s", sdk.OSArch(), *checksum)

    if _, err := client.RestoreCache(ctx, []string{key}); err != nil {
        return fmt.Errorf("restore cache: %w", err)
    }

    // ... do work ...

    return client.SaveCache(ctx, key, []string{"~/go/pkg/mod"})
}
```

Invoked from a config, the caller expands the template:

```yaml
- run: my-function --checksum '{{ checksum "go.sum" }}'
```

Cache keys are **write-once**: the first save under a key wins and later saves
under that key are skipped. Include something that changes when the cached
content should — a checksum, and `{{ epoch | round "72h" }}` if you want
rotation.

### Available operations

| Method | Returns | Description |
|---|---|---|
| `client.SaveCache(ctx, key, paths)` | `error` | Save paths under a cache key |
| `client.RestoreCache(ctx, keys)` | `(CacheResult, error)` | Restore the first matching cache key |
| `client.AtTeardown().SaveCache(ctx, key, paths)` | `error` | Register a cache save to run at the end of the job |
| `client.AtTeardown().OnFail()`, `.OnSuccess()` | `*Teardown` | Narrow a registration to a job outcome |
| `sdk.WithStepName(name)` | `CacheOption` | Name the step a save or restore creates |

`sdk.ErrNoServer` is returned when the function is not running inside a CircleCI job. Use `errors.Is(err, sdk.ErrNoServer)` to detect this.

A job shows a cache step as `Saving cache` or `Restoring cache`. Handle more than
one cache and those steps become indistinguishable, so name them:

```go
client.RestoreCache(ctx, keys, sdk.WithStepName("Restoring go build cache"))
```

### Saving at the end of the job

A cache usually needs a restore near the start and a matching save near the end. `AtTeardown` lets you register both in one place, so the key is written once:

```go
func run(ctx context.Context, client *sdk.Client) error {
    key := "go-modules-{{ checksum \"go.sum\" }}"

    if _, err := client.RestoreCache(ctx, []string{key}); err != nil {
        return fmt.Errorf("restore cache: %w", err)
    }

    // Registered here, saved once the job's steps are done.
    return client.AtTeardown().SaveCache(ctx, key, []string{"~/go/pkg/mod"})
}
```

Registered work runs at the end of the job in reverse order of registration, and still runs if the job is cancelled or times out. A failing teardown step fails the job, but a cache save never fails — it reports success whatever happens to it, exactly as an inline `SaveCache` does. `SaveCache` returns once the save is registered, so it reports nothing about whether the save itself succeeded.

Use `OnFail` or `OnSuccess` to register work for one job outcome only, for example collecting diagnostics that are only worth keeping when something broke:

```go
client.AtTeardown().OnFail().SaveCache(ctx, key, paths)
```

## Testing

`sdktest` gives you an in-memory cache and a sandbox, and needs nothing beyond
`go test` and `os/exec`. Reach for the sandbox by default: a function's defaults
point at the places a job owns — `$HOME`, `$BASH_ENV`, somewhere on `PATH`, a
cache key — and against those defaults a test does all of that to the machine
running it.

```go
import "github.com/circleci-functions/toolkit/sdk/sdktest"
```

### Unit test

`Client()` gives you a client whose cache operations stay in memory.

```go
func TestRun(t *testing.T) {
    srv := sdktest.NewServer(t)

    err := run(context.Background(), srv.Client())
    assert.NilError(t, err)
}
```

`srv.SavedKeys()` returns what the function saved, so you can assert it
partitions its keys the way you intended.

The in-memory cache mirrors CircleCI's behaviour:

- **Write-once keys.** A second save under a key already written is a no-op, not
  an overwrite. Use `sdktest.WithMutableKeys()` if a test deliberately needs the
  unrealistic behaviour.
- **Prefix matching on restore.** A restore key matches any saved key it is a
  prefix of, and takes the most recently saved among them. A restore tries its
  keys in order and stops at the first that matches anything, so a function can
  offer a list that broadens as it goes and warm-start from a near miss:

  ```go
  client.RestoreCache(ctx, []string{
      "v1-build-" + checksum + "-" + window, // exact
      "v1-build-" + checksum + "-",          // same deps, any window
      "v1-build-",                           // any deps
  })
  ```

### Saves registered for the end of the job

A save registered for the end of the job is recorded rather than performed, so a test decides when it happens. `srv.PendingCacheSaves()` reports what is registered and not yet run, and `srv.EndJob(t)` runs it as the job would:

```go
func TestRunSavesAtEndOfJob(t *testing.T) {
    srv := sdktest.NewServer(t)

    err := run(context.Background(), srv.Client())
    assert.NilError(t, err)
    assert.DeepEqual(t, srv.PendingCacheSaves(), []string{"go-modules-abc123"})

    srv.EndJob(t)
    // the cache is now saved, and a restore brings it back
}
```

`EndJob` ends a job whose steps succeeded, so work registered with `OnFail` is
discarded; `EndJobAfterFailure(t)` ends one whose steps failed.

### Subprocess test

`NewSandbox` gives you temp stand-ins for the places a function writes —
`Home`, `BashEnv`, `BinDir`, `InstallDir` — so nothing lands outside the test.
Pass `Env()` to the subprocess:

```go
func TestBinary(t *testing.T) {
    s := sdktest.NewSandbox(t)

    cmd := exec.CommandContext(t.Context(), binaryPath,
        "install", "--install-dir", s.InstallDir, "--bin-dir", s.BinDir,
    )
    cmd.Env = s.CmdEnv(os.Environ())

    out, err := cmd.CombinedOutput()
    assert.NilError(t, err, string(out))
}
```

`toolkit/testing/runner` takes the same map via `runner.WithSandboxEnv(s.Env())`.

`NewSandbox` does not change the test process's own environment, so a
subprocess-only suite can run in parallel. A test that exercises the function
in-process instead needs `s.ApplyToProcess(t)`, which points this process's
`$HOME` and `$BASH_ENV` at the sandbox for the duration of the test — and must
not then be parallel.

The sandbox has no opinion about what your function does, so add whatever else
it needs on top:

```go
env := maps.Clone(s.Env())
env["MY_TOOL_HOME"] = filepath.Join(s.Home, ".my-tool")
```

Watch for variables `go test` exports to the test process that a subprocess
should not inherit. A function that installs a Go toolchain wants `GOROOT`
cleared, for instance, since a toolchain honours it over its own location and
would otherwise run with the test runner's tools.

### Unsandboxed test

A sandbox is also what hides where a function puts things: pass `--bin-dir` and
the choice of directory on `PATH` — `~/go/bin` on one image, `/usr/local/bin`
on another — never happens. `SkipUnlessInJob` sanctions a test that runs
without one, so it runs only where the machine is disposable:

```go
func TestPutsToolOnPath(t *testing.T) {
    sdktest.SkipUnlessInJob(t)

    // No --bin-dir, so the function picks a directory off the real PATH.
    r := runner.New(t, binaryPath)
    _, err := r.Run("--version", "1.2.3")
    assert.NilError(t, err)

    out, err := sdktest.LaterStep(t, "mytool", "--version")
    assert.NilError(t, err, "a later step could not resolve mytool: %s", out)
}
```

`LaterStep` resolves a command through `PATH` in a fresh process, which is what
a later step amounts to once a function stops exporting to a shell. It drops
`$GOROOT`, for the reason the previous section gives. Run a shell as the command
to check one a function's exports never reach:

```go
out, err := sdktest.LaterStep(t, "powershell.exe", "-Command", "mytool --version")
```

### Cache keys in tests

Use `s.CacheKeyNonce` in any key a test saves under:

```go
key := "my-function-" + s.CacheKeyNonce
```

Keys are write-once, so a round-trip asserted under a fixed key passes on the
first run and then restores whatever that run left behind. The nonce is stable
within a sandbox — a save and its restore agree on it — and distinct across
sandboxes, tests, and job runs.

### Testing against CircleCI's own cache

The in-memory cache mirrors CircleCI's, but only the real one is authoritative.
`WithLiveCache` offers a sandbox the real cache, and whoever runs the tests
chooses:

```go
s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
if s.LiveCache() {
    t.Log("running against CircleCI's own cache")
}
```

```sh
# in-memory, the default — a laptop, or a CI job that wants no real dependencies
go test ./acceptance/...

# CircleCI's own cache — only works as a step inside a CircleCI job
TOOLKIT_CACHE_MODE=live go test ./acceptance/...
```

One suite is therefore exact in CI and still runs unchanged on a laptop. If
`live` is asked for and the real cache is not reachable, the test fails rather
than quietly using the in-memory one and reporting a pass on a behaviour it
never exercised.

Live-mode keys are scoped to the job run, so nothing a suite writes is visible
to another run — which also means each run leaves cache entries behind that
nothing will restore. Keep the paths small, and prefix the keys with something
recognisable so they are easy to spot.

`sdk.InCircleCIJob()` reports whether the privileged operations are reachable at
all, if you need to branch on that yourself.
