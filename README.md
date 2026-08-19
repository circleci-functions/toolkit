# toolkit

Shared Go code used by [CircleCI functions](https://github.com/circleci-functions) —
first-party, versioned step implementations invoked via `circleci run <name>@<version>`.

## Packages

| Package | Purpose |
|---|---|
| [`bashenv`](bashenv) | Append shell exports to `$BASH_ENV`, and read them back |
| [`sdk`](sdk) | Perform privileged CircleCI job operations (cache save/restore, etc.) without holding credentials |
| [`sdk/sdktest`](sdk/sdktest) | Fake CircleCI plugin server and test sandbox for functions that use `sdk` |
| [`functionyaml`](functionyaml) | Define, build, and validate the `function.yaml` schema |
| [`archive`](archive) | Helpers for writing files extracted from archives |
| [`testing/compiler`](testing/compiler) | Compile a function's binary once for a test suite |
| [`testing/runner`](testing/runner) | Run that binary as a subprocess the way production will |

`sdk` and `sdk/sdktest` depend only on the standard library, so a function can
take the SDK without taking the rest of the toolkit.

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
