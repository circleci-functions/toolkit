# toolkit

Shared Go code used by [CircleCI functions](https://github.com/circleci-functions) —
first-party, versioned step implementations invoked via `circleci run <name>@<version>`.

## Packages

| Package | Purpose |
|---|---|
| [`bashenv`](bashenv) | Append shell exports to `$BASH_ENV`, and read them back. Still the only way to export an arbitrary variable to later steps; use `pathlink` for putting a binary on `PATH` |
| [`pathlink`](pathlink) | Make a binary resolvable by name for the rest of a job, without a shell |
| [`sdk`](sdk) | Perform privileged CircleCI job operations (cache save/restore, etc.) without holding credentials |
| [`sdk/sdktest`](sdk/sdktest) | In-memory cache and test sandbox for functions that use `sdk` |
| [`functionyaml`](functionyaml) | Define, build, and validate the `function.yaml` schema |
| [`archive`](archive) | Helpers for writing files extracted from archives |
| [`testing/compiler`](testing/compiler) | Compile a function's binary once for a test suite |
| [`testing/runner`](testing/runner) | Run that binary as a subprocess the way production will |

`sdk` and `sdk/sdktest` depend only on the standard library, and `sdktest` needs
nothing beyond `go test` and `os/exec` to run a function's binary under test. A
function can take the SDK on its own and bring whatever else it likes — the
`testing` packages are one way to compile and run a binary in a suite, not a
requirement.

## `cmd/release`

Cross-compiles a function and publishes it to the `circleci-functions` S3 bucket. Run
from the root of a function's own repo:

```
go tool release --name <function-name> --version <version> [--dry-run]
```

Add it to a function repo's `go.mod` as a build tool:

```
tool (
	github.com/circleci-functions/toolkit/cmd/release
)
```

### Example config

Each release publishes an `example` in `function.yaml`: the config a user writes
to run the function, with the released version filled in. It passes every
required flag, plus any flag given an example value — with kong, a struct tag:

```go
Version string `help:"Go version spec." default:"stable" example:"1.22"`
```

```yaml
functions:
  setup-go: github.com/circleci-functions/setup-go@v0.5.4
jobs:
  build:
    steps:
      - setup-go:
          with:
            version: "1.22"
```

A function built without kong sets `example:` on the flag in a checked-in
`function.yaml` instead. `--dry-run` prints the example a release would publish,
and fails if a required flag has no example value.
