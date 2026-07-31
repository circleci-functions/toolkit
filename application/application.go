/*
Package application provides a managed solution for running a Go application and
building block helpers. It also provides a Component interface to
define/start/stop applications as their discrete components.

Use NewService to build a service app that minimizes boilerplate.

See docs/application.md for more information.
*/
package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/circleci-functions/toolkit/crypto"
	"github.com/circleci-functions/toolkit/environment"
	"github.com/circleci-functions/toolkit/health"
	"github.com/circleci-functions/toolkit/internal/vault"
	"github.com/circleci-functions/toolkit/observability"
	"github.com/circleci-functions/toolkit/observability/metrics/collector"
	"github.com/circleci-functions/toolkit/rundef"
)

// Component is an interface for application building blocks.
//
// The Run method is expected to start any long-running processes. The Run
// method must return when the given context is cancelled.
//
// The Teardown method tears down the Component. Teardown should always be
// called after component construction, even if Run has not been called. It can
// be a no-op if no cleanup is needed. Teardown should be idempotent.
//
// The context passed to the Run and Teardown methods should be used for
// timeouts and cancellations.
type Component interface {
	Run(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// ComponentFunc makes it easier to create a functional component
type ComponentFunc func(context.Context) error

// Run calls f(ctx).
func (f ComponentFunc) Run(ctx context.Context) error {
	return f(ctx)
}

// Teardown satisfies the Component interface.
func (f ComponentFunc) Teardown(context.Context) error {
	return nil
}

// WaitFunc is the type of function that blocks while the application runs
// during Run and before Teardown. WaitFunc should unblock and return `nil` for
// "happy" termination states (e.g. an expected termination signal), otherwise
// it should return an error.
type WaitFunc func(ctx context.Context) error

// runComponents runs the given components, asynchronously in arbitrary
// order. It then Runs the given jobs, asynchronously in arbitrary order,
// if any are provided.
//
// It blocks until:
//   - All jobs have returned (if any)
//   - The wait function has returned (if any)
//   - All components have returned (if no jobs)
//
// It Returns the first error encountered. Each Run method should ensure
// a result is promptly returned when the given context is cancelled. Panics
// that occur during the Run methods are bubbled up.
func runComponents(ctx context.Context, wait WaitFunc, cs []Component, shutdownDelay time.Duration) error {
	g, ctx := errgroup.WithContext(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if wait != nil {
		g.Go(func() error {
			// We cancel the context here because the wait function has been
			// returned
			defer cancel()
			err := wait(ctx)
			if err != nil {
				return err
			}

			if shutdownDelay != 0 {
				time.Sleep(shutdownDelay)
			}
			return nil
		})
	}

	errPanic := errors.New("panic during Run")
	for _, c := range cs {
		g.Go(func() (err error) {
			defer func() {
				if e := recover(); e != nil {
					err = fmt.Errorf("%w component %T: %+v", errPanic, c, e)
				}
			}()
			err = c.Run(ctx)
			if err != nil {
				return fmt.Errorf("error during component %T run: %w", c, err)
			}
			return err
		})
	}

	err := g.Wait()

	if err != nil {
		switch {
		case errors.Is(err, errPanic):
			// convert a panic inside the goroutine to a panic outside, so that all
			// defers happen as expected
			panic(err)
		case errors.Is(err, errRunJobSuccess):
			return nil
		}
	}
	return err
}

// TeardownComponents each component sequentially, in opposite order (right to
// left). Tears down all components, even if a Teardown method returns an
// error. Returns the last error encountered.
func TeardownComponents(ctx context.Context, cs []Component) error {
	var lastError error
	for i := len(cs) - 1; i >= 0; i-- {
		c := cs[i]
		err := c.Teardown(ctx)
		if err != nil {
			lastError = fmt.Errorf("component %T failed teardown: %w", c, err)
		}
	}
	return lastError
}

// BaseComponents are the Backplane-provided components that should be included
// in every application
func BaseComponents() []Component {
	c := collector.New()
	return []Component{c}
}

// ManagedApp is an application that expresses application-specific code as a
// set of Hooks that run at specific times (currently supports PreRunHooks)
type ManagedApp struct {
	env                 environment.Environment
	reporter            observability.Reporter
	encryptionProviders map[string]crypto.EncryptionProvider
	components          []Component
	readinessChecks     []ComponentReadiness
	preRunHooks         []Hook
	wait                WaitFunc
	shutdownDelay       bool
}

// NewJob returns a ManagedApp that has settings similar to a service
// application but with an empty wait function that doesn't block the
// application to exit.  Use SetMainJob to exit the application when a
// specific component finishes.
func NewJob(ctx context.Context, opts ...Option) (*ManagedApp, error) {
	return newProductionApp(ctx, opts...)
}

// NewService returns a ManagedApp that has production-esque settings
// already supplied: an OS environment, a Reporter, the base components, and a
// signal-based wait function
func NewService(ctx context.Context, opts ...Option) (*ManagedApp, error) {
	opts = append([]Option{WithWaitFunc(WaitForSignal)}, opts...)
	app, err := newProductionApp(ctx, opts...)
	if err != nil {
		return nil, err
	}
	app.shutdownDelay = true
	return app, nil
}

// ComponentReadiness allows declaring readiness checks for structs. Also see Component
type ComponentReadiness interface {
	ReadinessChecks() []health.Check
}

// Env returns the environment from a ManagedApp
func (app *ManagedApp) Env() environment.Environment {
	return app.env
}

// Reporter returns the reporter from a ManagedApp
func (app *ManagedApp) Reporter() observability.Reporter {
	return app.reporter
}

// AddHealthCheck adds a ComponentReadiness health check, for later aggregation into a http.Handler
func (app *ManagedApp) AddHealthCheck(cs ComponentReadiness) {
	app.readinessChecks = append(app.readinessChecks, cs)
}

// ReadinessHandler produces a http.Handler for the set of added ComponentReadiness checks
func (app *ManagedApp) ReadinessHandler() (http.Handler, error) {
	checks := make([]health.Check, 0, len(app.readinessChecks))
	for _, c := range app.readinessChecks {
		checks = append(checks, c.ReadinessChecks()...)
	}

	return health.BuildHandler(checks)
}

// Option is an function that modifies a ManagedApp
type Option func(app *ManagedApp)

// Hook is a function that runs at some point during the lifecycle of a
// ManagedApp
type Hook func(ctx context.Context, app *ManagedApp) error

// WithEnv uses the provided env as the environment for the application
func WithEnv(env environment.Environment) Option {
	return func(app *ManagedApp) {
		app.env = env
	}
}

// WithReporter uses the provided reporter as the reporter for the application
func WithReporter(r observability.Reporter) Option {
	return func(app *ManagedApp) {
		app.reporter = r
	}
}

// WithPreRunHook adds a hook that is called before the application Run; use
// this to add components to the application before it starts
func WithPreRunHook(hook Hook) Option {
	return func(app *ManagedApp) {
		app.preRunHooks = append(app.preRunHooks, hook)
	}
}

// WithWaitFunc uses the provided wait function to block the application while
// its running, see WaitForSignal
func WithWaitFunc(wait WaitFunc) Option {
	return func(app *ManagedApp) {
		app.wait = wait
	}
}

// WithEncryption uses the provided crypto.EncryptionProvider to
// provide enryption
func WithEncryption(e crypto.EncryptionProvider) Option {
	return func(app *ManagedApp) {
		app.encryptionProviders[""] = e
	}
}

// WaitForSignal blocks waiting for an interrupt/termination signal. It always
// returns `nil`, either when the signal arrives or when the context is
// cancelled.
func WaitForSignal(ctx context.Context) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-ctx.Done():
	}
	return nil
}

// New returns a new ManagedApp. See NewService to get a ManagedApp with
// production settings applied.
func New(_ context.Context, opts ...Option) (*ManagedApp, error) {
	app := &ManagedApp{
		reporter:            observability.NewCompositeReporter(),
		encryptionProviders: make(map[string]crypto.EncryptionProvider),
	}
	for _, opt := range opts {
		opt(app)
	}
	return app, nil
}

// ProductionEnvironment returns an application Environment based on the OS and
// any EXTRA_ENVIRONMENT_SOURCES. The OS is always the primary environment
// source and cannot be removed. Vault is currently the only additional source.
func ProductionEnvironment() (environment.Environment, error) {
	return ProductionEnvironmentFromBase(context.Background(), environment.FromOS())
}

// ProductionEnvironmentFromBase returns an application Environment
// based on the given environment and any EXTRA_ENVIRONMENT_SOURCES.
func ProductionEnvironmentFromBase(ctx context.Context, envBase environment.Environment) (environment.Environment, error) {
	extra := envBase.Get("EXTRA_ENVIRONMENT_SOURCES")
	if extra == "" {
		return envBase, nil
	}
	sources := []environment.Source{envBase}
	for _, source := range strings.Split(extra, ",") {
		switch source {
		case "vault":
			vaultCfg, err := loadVaultEnvSourceConfig(envBase)
			if err != nil {
				return nil, err
			}
			v, err := vault.NewEnvSource(ctx, vaultCfg)
			if err != nil {
				return nil, err
			}
			sources = append(sources, environment.FromSource(v))
		default:
			return nil, fmt.Errorf("unsupported environment source '%s'", source)
		}
	}
	return environment.FromAny(sources...), nil
}

func newProductionApp(ctx context.Context, opts ...Option) (_ *ManagedApp, err error) {
	envBase := environment.FromOS()
	if !observability.HasReporter(ctx) {
		repBase := observability.FallbackReporter(ctx, envBase)
		ctx = observability.WithReporter(ctx, repBase)
		defer func() {
			errTeardown := repBase.Teardown(ctx)
			if err == nil && errTeardown != nil {
				err = errTeardown
			}
		}()
	}
	// we do not ever want the stub reporter from New; only the one from
	// opts or the default production reporter created below
	noStubReporter := WithReporter(nil)

	app, err := New(ctx, append([]Option{noStubReporter}, opts...)...)
	if err != nil {
		return nil, err
	}
	if app.env == nil {
		app.env, err = ProductionEnvironmentFromBase(ctx, envBase)
		if err != nil {
			return nil, err
		}
	}
	if app.reporter == nil {
		app.reporter, err = observability.NewReporter(ctx, app.env)
		if err != nil {
			return nil, err
		}
	}
	for _, c := range BaseComponents() {
		app.AddComponent(c)
	}
	return app, nil
}

// AddComponent adds a component (or multiple components) to the application
// that whose Run and Teardown methods are called during the application's Run
// and Teardown methods, respectively
func (app *ManagedApp) AddComponent(cs ...Component) {
	app.components = append(app.components, cs...)
}

// SetMainJob adds a job  to the application.
// The difference between a job and a component is that a job
// short live and once all jobs are done running the application will automatically
// gracefully exit.
func (app *ManagedApp) SetMainJob(job Component) {
	app.AddComponent(&jobShim{job})
}

func (app *ManagedApp) startup(ctx context.Context) error {
	app.reporter.Info(ctx, "application startup")

	err := rundef.Defaults(ctx)
	if err != nil {
		return fmt.Errorf("error setting up runtime: %w", err)
	}

	for _, hook := range app.preRunHooks {
		err := hook(ctx, app)
		if err != nil {
			return fmt.Errorf("pre startup hook error: %w", err)
		}
	}

	var shutdownDelay time.Duration
	if app.shutdownDelay {
		shutdownDelay = getDelay(app.env)
	}

	err = runComponents(ctx, app.wait, app.components, shutdownDelay)
	if err != nil {
		return fmt.Errorf("error running app: %w", err)
	}

	return nil
}

func getDelay(env environment.Environment) time.Duration {
	delay, err := time.ParseDuration(env.Get("SHUTDOWN_DELAY"))
	if err != nil {
		// fall back to default
		return 15 * time.Second
	}
	return delay
}

func (app *ManagedApp) teardownComponents(ctx context.Context) error {
	return TeardownComponents(ctx, app.components)
}

func (app *ManagedApp) teardownReporter(ctx context.Context) error {
	if app.reporter != nil {
		if err := app.reporter.Teardown(ctx); err != nil {
			app.reporter = nil
			FatalError(err)
			return err
		}
		app.reporter = nil
	}
	return nil
}

// Execute starts the application, waits until either the application's WaitFunc
// returns or a component Run method errors, then tears down the application
func (app *ManagedApp) Execute(ctx context.Context) (err error) {
	// Prefer any explicit reporter provided in the context
	ok := observability.HasReporter(ctx)
	if !ok {
		ctx = observability.WithReporter(ctx, app.reporter)
	}

	defer func() {
		// do not tear down the reporter if we need it imminently to
		// report an application error; it is torn down automatically
		// before sending the Fatal
		if err == nil {
			err = app.teardownReporter(ctx)
		}
	}()
	defer app.reporter.ReportRecoveredPanic(ctx)
	defer func() {
		errTeardown := app.teardownComponents(ctx)
		if err == nil && errTeardown != nil {
			err = errTeardown
		}
	}()
	return app.startup(ctx)
}

// FatalError creates a fallback Reporter from the OS environment and exits the
// application with a status code of 1. Should only be used from the main()
// function.
func FatalError(err error) {
	fallback := observability.FallbackReporter(context.Background(), environment.FromOS())
	fallback.FatalWithError(context.Background(), "fatal application error", err)
}

// FatalError method exits the application with the reporter's FatalWithError,
// if the application's reporter has not been torn down. Otherwise, exits with
// the FatalError function. Should only be used from the main() function.
func (app *ManagedApp) FatalError(ctx context.Context, err error) {
	if app.reporter != nil {
		app.reporter.FatalWithError(ctx, "fatal application error", err)
	}
	FatalError(err)
}

// Encryption returns a crypto.EncryptionProvider for the application's
// default key. Equivalent to EncryptionWithKey(ctx, "").
func (app *ManagedApp) Encryption(ctx context.Context) (crypto.EncryptionProvider, error) {
	return app.EncryptionWithKey(ctx, "")
}

// EncryptionWithKey returns a crypto.EncryptionProvider for the key
// identified by suffix. Use it when a service needs more than one
// encryption key.
func (app *ManagedApp) EncryptionWithKey(ctx context.Context, suffix string) (crypto.EncryptionProvider, error) {
	if e, ok := app.encryptionProviders[suffix]; ok {
		return e, nil
	}

	p := app.env.Get("ENCRYPTION_PROVIDER")
	switch p {
	case "vault":
		cfg, err := loadVaultEncryptorConfig(ctx, app.env, suffix)
		if err != nil {
			return nil, fmt.Errorf("vault configuration failed: %w", err)
		}
		v, err := vault.NewEncryptor(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("vault creation failed: %w", err)
		}
		app.AddComponent(v)
		app.encryptionProviders[suffix] = v
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported encryption provider '%s'", p)
	}
}

var errRunJobSuccess = errors.New("job done")

type jobShim struct {
	job Component
}

func (j *jobShim) Run(ctx context.Context) error {
	err := j.job.Run(ctx)
	if err != nil {
		return err
	}
	return errRunJobSuccess
}

func (j *jobShim) Teardown(ctx context.Context) error {
	return j.job.Teardown(ctx)
}
