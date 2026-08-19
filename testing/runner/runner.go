package runner

import (
	"bytes"
	"io"
	"maps"
	"os"
	"os/exec"
	"testing"
)

// Runner allows for running compiled subcommand binaries in tests in a way that is consitent with how they will be
// invoked in prod.
type Runner struct {
	bin string

	useOSEnvVars bool
	envVars      map[string]string

	stdout *bytes.Buffer
	stderr *bytes.Buffer
	cmdDir string
}

type Opt func(*Runner)

func WithBaseURL(baseURL string) Opt {
	return func(r *Runner) {
		r.envVars["CIRCLE_URL"] = baseURL
	}
}

func WithTokenSocket(socket string) Opt {
	return func(r *Runner) {
		r.envVars["CIRCLE_TASK_TOKEN_SOCKET"] = socket
	}
}

func WithExO11YURL(url string) Opt {
	return func(r *Runner) {
		r.envVars["CIRCLE_EX_O11Y_URL"] = url
	}
}

func WithEnterprise() Opt {
	return func(r *Runner) {
		r.envVars["CIRCLE_ENTERPRISE"] = "true"
	}
}

// WithUnsafeAdditionalEnvVars sets any arbitrary additional env vars to be passed to the runner.
// This is considered unsafe as this will not be the case when the subcommand runs in production and should therefore
// be used with caution.
func WithUnsafeAdditionalEnvVars(envVars map[string]string) Opt {
	return func(r *Runner) {
		for k, v := range envVars {
			r.envVars[k] = v
		}
	}
}

// WithUnsafeOSEnvVars sets the runner to inherit the environment variables from the parent process.
// This is considered unsafe as this will not be the case when the subcommand runs in production and should therefore
// be used with caution.
func WithUnsafeOSEnvVars() Opt {
	return func(r *Runner) {
		r.useOSEnvVars = true
	}
}

// WithSandboxEnv inherits the parent environment and then applies env on top, so
// env wins. Pass sdktest.Sandbox.Env or EnvWith to run the binary against temp
// stand-ins for $HOME, $BASH_ENV and the cache server rather than the real ones.
func WithSandboxEnv(env map[string]string) Opt {
	return func(r *Runner) {
		r.useOSEnvVars = true
		maps.Copy(r.envVars, env)
	}
}

// WithCmdDir sets the working directory for the command being run.
func WithCmdDir(cmdDir string) Opt {
	return func(r *Runner) {
		r.cmdDir = cmdDir
	}
}

func New(t *testing.T, bin string, opts ...Opt) *Runner {
	t.Helper()

	r := &Runner{
		bin: bin,
		envVars: map[string]string{
			// Env vars that are passed through in prod
			"PATH":   os.Getenv("PATH"),
			"TMPDIR": os.Getenv("TMPDIR"),
			"TMP":    os.Getenv("TMP"),
			"TEMP":   os.Getenv("TEMP"),
		},
		cmdDir: t.TempDir(),
	}

	for _, opt := range opts {
		opt(r)
	}

	r.Reset()

	return r
}

func (r *Runner) Run(args ...string) (int, error) {
	//nolint:gosec
	cmd := exec.Command(r.bin, args...)
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	cmd.Dir = r.cmdDir

	if r.useOSEnvVars {
		cmd.Env = os.Environ()
	}

	for k, v := range r.envVars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	return code, err
}

func (r *Runner) Stdout() io.Reader {
	return r.stdout
}

func (r *Runner) StdoutString() string {
	return r.stdout.String()
}

func (r *Runner) Stderr() io.Reader {
	return r.stderr
}

func (r *Runner) StderrString() string {
	return r.stderr.String()
}

func (r *Runner) CmdDir() string {
	return r.cmdDir
}

func (r *Runner) Reset() {
	r.stdout = bytes.NewBufferString("")
	r.stderr = bytes.NewBufferString("")
}
