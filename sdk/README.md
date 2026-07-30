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

`sdk.ErrNoServer` is returned when the function is not running inside a CircleCI job. Use `errors.Is(err, sdk.ErrNoServer)` to detect this.

## Testing

Import `sdktest` to start a fake server in your tests. Use `srv.Client()` to get a pre-configured client, or `srv.SetupEnv(t)` when your test runs the function binary as a subprocess.

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
