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

`sdktest` gives you an in-memory cache and a sandbox. Reach for the sandbox by
default: a function that installs tools writes to `$HOME`, symlinks onto `PATH`
and saves caches, and against real defaults a test does all of that to the
machine running it.

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

### Subprocess test

`NewSandbox` redirects `$HOME` and `$BASH_ENV` at temp directories and gives you
`BinDir` and `InstallDir` to pass to your function's flags, so nothing lands
outside the test. `Command` runs your binary inside it:

```go
func TestBinary(t *testing.T) {
    s := sdktest.NewSandbox(t)

    out, err := s.Command(t.Context(), binaryPath,
        "install", "--install-dir", s.InstallDir, "--bin-dir", s.BinDir,
    ).CombinedOutput()
    assert.NilError(t, err, string(out))
}
```

Use `s.CacheChecksum()` for anything that round-trips a save and restore. It is
unique per run, which a constant key is not — see the write-once note above.

### Testing against CircleCI's own cache

The in-memory cache mirrors CircleCI's behaviour, but only the real one is
authoritative. `WithLiveCache` uses it when the test runs inside a CircleCI job,
and the in-memory one everywhere else, so a single suite is exact in CI and still
runs on a laptop:

```go
s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
if s.Cache != nil {
    t.Log("running against the in-memory cache")
}
```

`sdk.InCircleCIJob()` reports the same condition if you need to branch on it
yourself.
