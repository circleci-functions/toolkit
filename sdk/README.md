# CircleCI Functions SDK

The SDK lets a CircleCI function perform privileged job operations — saving and restoring caches, storing artifacts, and more — without holding its own credentials.

## Usage

Call `sdk.Run` in `main`. It handles signal shutdown and passes a ready-to-use `*Client` to your function.

```go
package main

import (
    "context"
    "fmt"

    "github.com/circleci-functions/toolkit/sdk"
)

func main() {
    sdk.Run(run)
}

func run(ctx context.Context, client *sdk.Client) error {
    _, err := client.RestoreCache(ctx, []string{"go-modules-{{ checksum \"go.sum\" }}"})
    if err != nil {
        return fmt.Errorf("restore cache: %w", err)
    }

    // ... do work ...

    paths := []string{"~/go/pkg/mod"}
    return client.SaveCache(ctx, "go-modules-{{ checksum \"go.sum\" }}", paths)
}
```

### Available operations

| Method | Returns | Description |
|---|---|---|
| `client.SaveCache(ctx, key, paths)` | `error` | Save paths under a cache key |
| `client.RestoreCache(ctx, keys)` | `(CacheResult, error)` | Restore the first matching cache key |
| `client.AtTeardown().SaveCache(ctx, key, paths)` | `error` | Register a cache save to run at the end of the job |
| `client.AtTeardown().OnFail()`, `.OnSuccess()` | `*Teardown` | Narrow a registration to a job outcome |

`sdk.ErrNoServer` is returned when the function is not running inside a CircleCI job. Use `errors.Is(err, sdk.ErrNoServer)` to detect this.

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

Import `sdktest` to start an in-memory cache in your tests. Use `srv.Client()` to get a pre-configured client, or `srv.SetupEnv(t)` when your test runs the function binary as a subprocess.

Its behaviour mirrors CircleCI's, including **write-once keys**: a second save
under a key already written is a no-op, not an overwrite. A round-trip asserted
under a constant key therefore passes on the first run and then restores
whatever that run left behind — partition the key with something that changes.
Use `sdktest.WithMutableKeys()` if a test deliberately needs the unrealistic
behaviour.

`srv.SavedKeys()` returns the keys written so far, so you can assert a function
partitions its keys the way you intended.

```go
import "github.com/circleci-functions/toolkit/sdk/sdktest"
```

### Unit test

```go
func TestRun(t *testing.T) {
    srv := sdktest.NewServer(t)

    err := run(context.Background(), srv.Client())
    assert.NilError(t, err)
}
```

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

### Subprocess / integration test

When running the compiled binary as a subprocess, call `srv.SetupEnv(t)` before starting the process. It injects the fake server URL into the test environment so the binary connects to it automatically.

```go
func TestBinary(t *testing.T) {
    srv := sdktest.NewServer(t)
    srv.SetupEnv(t)

    cmd := exec.CommandContext(ctx, binaryPath, "--some-flag")
    out, err := cmd.CombinedOutput()
    assert.NilError(t, err, string(out))
}
```
