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

`sdk.ErrNoServer` is returned when the function is not running inside a CircleCI job. Use `errors.Is(err, sdk.ErrNoServer)` to detect this.

## Testing

`sdktest` provides a fake plugin server and a sandbox. Reach for the sandbox by
default: a function that installs tools writes to `$HOME`, symlinks onto `PATH`
and saves caches, and against real defaults a test does all of that to the
machine running it.

```go
import "github.com/circleci-functions/toolkit/sdk/sdktest"
```

### Unit test

`Client()` gives you a client wired to the fake server.

```go
func TestRun(t *testing.T) {
    srv := sdktest.NewServer(t)

    err := run(context.Background(), srv.Client())
    assert.NilError(t, err)
}
```

### Subprocess test

`NewSandbox` redirects `$HOME` and `$BASH_ENV` at temp directories and gives you
`BinDir` and `InstallDir` to pass to your function's flags, so nothing lands
outside the test. Combine it with `testing/runner`:

```go
func TestBinary(t *testing.T) {
    s := sdktest.NewSandbox(t)

    r := runner.New(t, binaryPath, runner.WithSandboxEnv(s.Env()))
    _, err := r.Run("install", "--install-dir", s.InstallDir, "--bin-dir", s.BinDir)
    assert.NilError(t, err, r.StderrString())

    path, err := bashenv.Path(s.BashEnv)  // what a later job step would see
    assert.NilError(t, err)
}
```

Use `s.CacheChecksum()` for anything that round-trips a real save and restore.
It is unique per run, which a constant key is not — see the write-once note
above.

### Testing against the real task-agent

`sdktest.WithRealAgent()` uses the task-agent when the test runs inside a
CircleCI job, and the fake otherwise, so one suite can be honest in CI and still
run on a laptop. `Sandbox.Server` is nil when the real agent is in use.

```go
s := sdktest.NewSandbox(t, sdktest.WithRealAgent())
```

`sdk.ServerAvailable()` reports whether an agent is reachable if you need to
branch on it yourself.
