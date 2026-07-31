package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/go-playground/validator/v10"
	ototel "go.opentelemetry.io/otel"

	"github.com/circleci-functions/toolkit/environment"
	"github.com/circleci-functions/toolkit/internal/httpmetrics"
	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/internal/statsd"
	"github.com/circleci-functions/toolkit/observability/noop"
	"github.com/circleci-functions/toolkit/observability/otel"
	"github.com/circleci-functions/toolkit/observability/rollbar"
	"github.com/circleci-functions/toolkit/observability/slog"
	"github.com/circleci-functions/toolkit/secret"
)

// ReporterConfig configures a production reporter
type ReporterConfig struct {
	LogDestination string `env:"LOG_DESTINATION,default=stdout" validate:"oneof=stdout stderr"`
	LogLevel       string `env:"LOG_LEVEL,default=info" validate:"oneof=debug info warn error panic fatal"`
	LogFormat      string `env:"LOG_FORMAT,default=color" validate:"oneof=color json text"`

	ErrorReportDestination string `env:"ERROR_REPORT_DESTINATION,default=none" validate:"oneof=none stderr rollbar"`
	ErrorReportLevel       string `env:"ERROR_REPORT_LEVEL,default=error" validate:"oneof=debug info warn error panic fatal"`
	RollbarToken           secret.String
	RollbarEndpoint        string `env:"ROLLBAR_ENDPOINT"`
	Hostname               string

	MetricDestination string        `env:"METRICS_DESTINATION,default=none" validate:"oneof=none statsd http"`
	StatsDHost        string        `env:"STATSD_HOST"`
	StatsDPort        int           `env:"STATSD_PORT,default=8125"`
	HTTPMetricsURL    string        `env:"HTTP_METRICS_URL,default="`
	HTTPMetricsToken  secret.String `env:"HTTP_METRICS_TOKEN,default="`

	TraceDestinations []string `env:"TRACE_DESTINATION,default=none" validate:"dive,oneof=none otel log"`
	CollectorProtocol string   `env:"OTEL_PROTOCOL,default=grpc" validate:"oneof='' 'grpc' 'http'"`
	CollectorSecure   bool     `env:"OTEL_COLLECTOR_SECURE,default=false"`
	CollectorHost     string   `env:"OTEL_COLLECTOR_HOST"`
	CollectorPort     int      `env:"OTEL_COLLECTOR_PORT,default=4317"`
	// CollectorGRPCPort is deprecated, move to OTEL_COLLECTOR_PORT
	CollectorGRPCPort int           `env:"OTEL_COLLECTOR_GRPC_PORT"`
	CollectorPath     string        `env:"OTEL_COLLECTOR_PATH"`
	CollectorToken    secret.String `env:"OTEL_COLLECTOR_TOKEN"`

	Resource Resource

	Warnings []error

	ExecutionCustomTracer report.TraceEmitter // Used during the cut over from execution to backplane
}

// Resource describes the resource for this service.
type Resource struct {
	DisableEnvironments   bool
	ServiceName           string
	Revision              string
	DeployEnvironment     string
	K8sNodeName           string
	K8sPodName            string
	K8sPodNamespace       string
	K8sReplicaSet         string
	K8sServiceAccountName string
}

// toFields returns Fields corresponding to the resource attributes
func (r Resource) toFields() Fields {
	fields := Fields{
		"canary":                   r.DeployEnvironment == "canary",
		"deploy_environment":       r.DeployEnvironment,
		"k8s_node_name":            r.K8sNodeName,
		"k8s_pod_name":             r.K8sPodName,
		"k8s_pod_namespace":        r.K8sPodNamespace,
		"k8s_replicaset":           r.K8sReplicaSet,
		"k8s_service_account_name": r.K8sServiceAccountName,
		"revision":                 r.Revision,
		"service":                  r.ServiceName,
	}
	for k, v := range fields {
		if v == "" || v == nil {
			delete(fields, k)
		}
	}
	return fields
}

func metaToResource(m environment.BaseMetadata) Resource {
	return Resource{
		ServiceName:           m.ServiceName,
		Revision:              m.Revision,
		DeployEnvironment:     m.DeployEnvironment,
		K8sNodeName:           m.K8sNodeName,
		K8sPodName:            m.K8sPodName,
		K8sPodNamespace:       m.K8sPodNamespace,
		K8sReplicaSet:         m.K8sReplicaSet,
		K8sServiceAccountName: m.K8sServiceAccountName,
	}
}

// NewReporter initializes a CompositeReporter component from an environment.
// Always call Teardown after construction to make sure everything is flushed
// and no data are lost.
func NewReporter(ctx context.Context, env environment.Environment) (*CompositeReporter, error) {
	cfg, err := NewReporterConfig(ctx, env)
	if err != nil {
		return nil, err
	}
	return NewReporterFromConfig(ctx, cfg)
}

// NewReporterConfig creates a new ReporterConfig from the environment
func NewReporterConfig(ctx context.Context, env environment.Environment) (ReporterConfig, error) {
	cfg := ReporterConfig{}
	err := loadReporterConfig(ctx, env, &cfg)
	if err != nil {
		return ReporterConfig{}, err
	}

	if cfg.CollectorPort == 0 && cfg.CollectorGRPCPort != 0 {
		cfg.CollectorPort = cfg.CollectorGRPCPort
	}

	err = validateReporterConfig(cfg)
	if err != nil {
		return ReporterConfig{}, err
	}
	return cfg, nil
}

// NewReporterFromConfig creates a new reporter from configuration
//
//nolint:cyclop,gocyclo,funlen,gocognit // This is just a lot of config setup and just a lot of if statements.
func NewReporterFromConfig(ctx context.Context, cfg ReporterConfig) (*CompositeReporter, error) {
	err := validateReporterConfig(cfg)
	if err != nil {
		return nil, err
	}

	var logLevel report.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = report.DebugLevel
	case "info":
		logLevel = report.InfoLevel
	case "warn":
		logLevel = report.WarnLevel
	case "error":
		logLevel = report.ErrorLevel
	case "panic":
		logLevel = report.PanicLevel
	case "fatal":
		logLevel = report.FatalLevel
	}

	var logFormat report.LogFormat
	switch cfg.LogFormat {
	case "color":
		logFormat = report.ColorLogFormat
	case "json":
		logFormat = report.JSONLogFormat
	case "text":
		logFormat = report.TextLogFormat
	}

	logCfg := slog.LoggerConfig{
		Level:  logLevel,
		Format: logFormat,
	}
	switch cfg.LogDestination {
	case "stdout":
		logCfg.Output = os.Stdout
	case "stderr":
		logCfg.Output = os.Stderr
	default:
		return nil, fmt.Errorf("invlaid log destination: %s", cfg.LogDestination)
	}
	logEmitter, err := slog.NewLogger(logCfg)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	baseFields := cfg.Resource.toFields()
	logReporter := NewCompositeReporter(
		WithBaseFields(baseFields),
		WithLogger(logEmitter),
	)

	var errLevel report.Level
	switch cfg.ErrorReportLevel {
	case "debug":
		errLevel = report.DebugLevel
	case "info":
		errLevel = report.InfoLevel
	case "warn":
		errLevel = report.WarnLevel
	case "error":
		errLevel = report.ErrorLevel
	case "panic":
		errLevel = report.PanicLevel
	case "fatal":
		errLevel = report.FatalLevel
	}

	var errorEmitter report.ErrorEmitter
	switch cfg.ErrorReportDestination {
	case destinationNone:
		errorEmitter = noop.Emitter
	case "stderr":
		loggerErr, err := slog.NewLogger(slog.LoggerConfig{
			Level:  logLevel,
			Format: logFormat,
			Output: os.Stderr,
		})
		if err != nil {
			return nil, err
		}
		errorEmitter = &report.LogToError{LogEmitter: loggerErr}
	case destinationRollbar:
		errorEmitter = rollbar.NewClient(rollbar.ClientConfig{
			Token:       cfg.RollbarToken,
			Endpoint:    cfg.RollbarEndpoint,
			Environment: cfg.Resource.DeployEnvironment,
			CodeVersion: cfg.Resource.Revision,
			ServerHost:  cfg.Hostname,
			Level:       errLevel,
		})
	}

	var metricEmitter report.MetricEmitter
	switch cfg.MetricDestination {
	case destinationNone:
		metricEmitter = noop.Emitter
	case "statsd":
		metricEmitter, err = statsd.NewClient(statsd.ClientConfig{
			Host: cfg.StatsDHost,
			Port: cfg.StatsDPort,
		})
		if err != nil {
			return nil, err
		}
	case "http":
		metricEmitter = httpmetrics.New(httpmetrics.Config{
			URL:        cfg.HTTPMetricsURL,
			AuthToken:  cfg.HTTPMetricsToken,
			ClientName: cfg.Resource.ServiceName,
		})
	}

	var traceEmitter report.TraceEmitter
	if cfg.ExecutionCustomTracer != nil {
		traceEmitter = cfg.ExecutionCustomTracer
	} else {
		if len(cfg.TraceDestinations) == 1 && cfg.TraceDestinations[0] == destinationNone {
			traceEmitter = noop.Emitter
		} else {
			tpCfg := otel.TracerProviderConfig{
				Resource: otel.ResourceConfig{
					UseEnvironments:       !cfg.Resource.DisableEnvironments,
					ServiceName:           cfg.Resource.ServiceName,
					Revision:              cfg.Resource.Revision,
					DeployEnvironment:     cfg.Resource.DeployEnvironment,
					K8sNodeName:           cfg.Resource.K8sNodeName,
					K8sPodName:            cfg.Resource.K8sPodName,
					K8sPodNamespace:       cfg.Resource.K8sPodNamespace,
					K8sReplicaSet:         cfg.Resource.K8sReplicaSet,
					K8sServiceAccountName: cfg.Resource.K8sServiceAccountName,
				},
			}
			for _, d := range cfg.TraceDestinations {
				switch d {
				case "log":
					tpCfg.Batchers = append(tpCfg.Batchers, otel.SpanEmitterExportConfig{
						SpanEmitter: &report.LogToSpan{LogEmitter: logEmitter},
					})
				case "otel":
					tpCfg.Batchers = append(tpCfg.Batchers, otel.CollectorExportConfig{
						Protocol: cfg.CollectorProtocol,
						Secure:   cfg.CollectorSecure,
						Host:     cfg.CollectorHost,
						Port:     cfg.CollectorPort,

						HTTPPath:        cfg.CollectorPath,
						HTTPToken:       cfg.CollectorToken,
						HTTPServiceName: cfg.Resource.ServiceName,
						HTTPRevision:    cfg.Resource.Revision,
					})
				}
			}
			traceEmitter, err = otel.NewTracerProvider(ctx, tpCfg)
			if err != nil {
				return nil, err
			}
		}
	}

	r := NewCompositeReporter(
		WithBaseFields(baseFields),
		WithLogger(logEmitter),
		WithErrorHandler(errorEmitter),
		WithMetricsEmitter(metricEmitter),
		WithTracer(traceEmitter),
	)

	errorEmitter.SendDeployment()

	// sets the global otel error handler
	ototel.SetErrorHandler(&tracerErrorHandler{logReporter})

	return r, nil
}

// FallbackReporter is a reporter that can be used to loudly exit the
// application if a NewReporter cannot be created
func FallbackReporter(ctx context.Context, env environment.Environment) *CompositeReporter {
	cfg := ReporterConfig{
		LogDestination:         "stdout",
		LogLevel:               "info",
		LogFormat:              "json",
		ErrorReportDestination: "stderr",
		ErrorReportLevel:       "error",
		MetricDestination:      "none",
		TraceDestinations:      []string{"log"},
		Resource:               metaToResource(env.GetBaseMetadata()),
	}
	if r, err := NewReporterFromConfig(ctx, cfg); err == nil {
		return r
	}
	logEmitter, _ := slog.NewLogger(slog.LoggerConfig{
		Level:  report.InfoLevel,
		Format: report.JSONLogFormat,
	})
	return NewCompositeReporter(
		WithBaseFields(cfg.Resource.toFields()),
		WithLogger(logEmitter),
	)
}

// anything that pulls new data from the environment, does not validate
func loadReporterConfig(ctx context.Context, env environment.Environment, cfg *ReporterConfig) error {
	err := env.Process(ctx, cfg)
	if err != nil {
		return err
	}
	meta := env.GetBaseMetadata()
	cfg.Resource = metaToResource(meta)
	if cfg.ErrorReportDestination == destinationRollbar {
		cfg.RollbarToken, err = rollbarToken(env)
		if err != nil {
			return err
		}
		hostname, err := os.Hostname()
		if err != nil {
			cfg.Warnings = append(cfg.Warnings, fmt.Errorf("cant get hostname: %w", err))
		} else {
			cfg.Hostname = hostname
		}
	}
	cfg.Resource.DisableEnvironments = strings.ToLower(env.Get("DISABLE_ENVIRONMENTS")) == "true"
	return nil
}

// anything that validates the config, does not pull new data
func validateReporterConfig(cfg ReporterConfig) error {
	validate := validator.New()
	err := validate.Struct(cfg)
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		for _, err := range ve {
			if err.Tag() == "oneof" {
				return fmt.Errorf("invalid %s: got '%s' expected one of (%s)",
					envTagFromStructField(reflect.TypeOf(cfg), err.StructField()),
					err.Value(),
					err.Param())
			}
		}

		return err
	}
	if cfg.ErrorReportDestination == destinationRollbar {
		if cfg.RollbarToken.Value() == "" {
			return errors.New("missing required rollbar token")
		}
	}
	if cfg.MetricDestination == "statsd" {
		if cfg.StatsDHost == "" {
			return errors.New("missing required STATSD_HOST")
		}
		if cfg.StatsDPort == 0 {
			return errors.New("missing required STATSD_PORT")
		}
	}
	if slices.Contains(cfg.TraceDestinations, "otel") {
		if cfg.CollectorHost == "" {
			return errors.New("missing required OTEL_COLLECTOR_HOST")
		}
		if cfg.CollectorPort == 0 {
			return errors.New("missing required OTEL_COLLECTOR_PORT")
		}
	}
	return nil
}

func envTagFromStructField(t reflect.Type, n string) string {
	fieldName := strings.Split(n, "[")
	if len(fieldName) == 0 {
		return n
	}

	f, ok := t.FieldByName(fieldName[0])
	if !ok {
		return n
	}

	tag := f.Tag.Get("env")
	if tag == "" {
		return n
	}

	env := strings.Split(tag, ",")
	if len(env) == 0 {
		return n
	}

	return env[0]
}

// rollbarToken gets the rollbar token from the environment, at one of the following paths:
// - <SERVICE_NAME>_ROLLBAR_TOKEN
// - ROLLBAR_TOKEN
func rollbarToken(env environment.Environment) (secret.String, error) {
	lookedFor := []string{}
	service := env.GetBaseMetadata().ServiceName
	if service != "" {
		prefix := strings.ReplaceAll(strings.ToUpper(service), "-", "_")
		key := prefix + "_" + "ROLLBAR_TOKEN"
		if token, ok := env.LookupSecret(key); ok {
			return token, nil
		}
		lookedFor = append(lookedFor, key)
	}
	if token, ok := env.LookupSecret("ROLLBAR_TOKEN"); ok {
		return token, nil
	}
	lookedFor = append(lookedFor, "ROLLBAR_TOKEN")
	err := fmt.Errorf("missing required rollbar token, looked in %s", strings.Join(lookedFor, " and "))
	return secret.NewString(""), err
}

const (
	destinationNone    = "none"
	destinationRollbar = "rollbar"
)
