// Command release cross-compiles a single function and publishes it to the
// circleci-functions S3 bucket. It's run from the root of a function's own
// repo (one function per repo).
//
// Each release publishes the gzipped binaries at
// github.com/circleci-functions/<name>/<version>/<os>/<arch>/<name> plus
// checksums.txt + release.txt. The <os>/<arch> layout is normalized here so
// one downloader can serve binaries regardless of source language.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/alecthomas/kong"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.yaml.in/yaml/v4"

	"github.com/circleci-functions/toolkit/compiler"
	"github.com/circleci-functions/toolkit/functionyaml"
	"github.com/circleci-functions/toolkit/releaser"
)

// defaultPlatforms is the set of OS/arch targets functions are built for.
var defaultPlatforms = map[string][]string{
	"linux":   {"amd64", "arm", "arm64", "ppc64le", "s390x"},
	"darwin":  {"amd64", "arm64"},
	"windows": {"amd64", "arm64"},
}

// namespace prefixes every published function's S3 key, mirroring the
// GitHub org each function's repo lives in.
const namespace = "github.com/circleci-functions/"

// Releaser compiles a function for each configured platform and publishes it to S3.
type Releaser struct {
	name    string
	version string
	bucket  string

	platforms map[string][]string
	client    *s3.Client
}

type Config struct {
	Name    string
	Version string
	Bucket  string

	// Platforms is an optional map of OS to architectures. Defaults to defaultPlatforms.
	Platforms map[string][]string

	// Client is an optional S3 client. Defaults to one built from the ambient AWS config.
	Client *s3.Client
}

func newReleaser(ctx context.Context, cfg Config) (Releaser, error) {
	if cfg.Name == "" || cfg.Version == "" {
		return Releaser{}, fmt.Errorf("name and version must be provided")
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "circleci-functions"
	}
	if len(cfg.Platforms) == 0 {
		cfg.Platforms = defaultPlatforms
	}
	if cfg.Client == nil {
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return Releaser{}, fmt.Errorf("load aws config: %w", err)
		}
		cfg.Client = s3.NewFromConfig(awsCfg)
	}
	return Releaser{
		name:      cfg.Name,
		version:   cfg.Version,
		bucket:    cfg.Bucket,
		platforms: cfg.Platforms,
		client:    cfg.Client,
	}, nil
}

// Run compiles the function and publishes it to S3. Pass dryRun to build
// artifacts locally without uploading.
func (r Releaser) Run(ctx context.Context, dryRun bool) error {
	if dryRun {
		return r.dryRun(ctx)
	}

	buildDir, err := os.MkdirTemp("", "functions-release")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	if err := r.build(ctx, buildDir); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	functionYAML, err := r.describe(ctx, buildDir)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}

	rel := releaser.NewWithClient(r.client)

	err = rel.Publish(ctx, releaser.PublishParameters{
		Path:    filepath.Join(buildDir, r.name),
		Bucket:  r.bucket,
		App:     namespace + r.name,
		Version: r.version,
	})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	// Direct upload — rel.Publish's Path walk would gzip this as a binary.
	key := filepath.ToSlash(filepath.Join(namespace, r.name, r.version, "function.yaml"))
	_, err = r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &r.bucket,
		Key:         &key,
		Body:        bytes.NewReader(functionYAML),
		ContentType: aws.String("text/yaml; charset=utf-8"),
	})
	if err != nil {
		return fmt.Errorf("upload function.yaml: %w", err)
	}

	err = rel.Release(ctx, releaser.ReleaseParameters{
		Bucket:  r.bucket,
		App:     namespace + r.name,
		Version: r.version,
	})
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}

	fmt.Printf("published %s%s %s to %s\n", namespace, r.name, r.version, r.bucket)
	return nil
}

// dryRun builds binaries into ./dist and prints the S3 keys that a real release
// would publish — no AWS credentials needed.
func (r Releaser) dryRun(ctx context.Context) error {
	outDir := "dist"
	if err := os.RemoveAll(filepath.Join(outDir, r.name)); err != nil {
		return err
	}
	if err := r.build(ctx, outDir); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	functionYAML, err := r.describe(ctx, outDir)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}
	yamlPath := filepath.Join(outDir, r.name, r.version, "function.yaml")
	if err := os.MkdirAll(filepath.Dir(yamlPath), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(yamlPath, functionYAML, 0o600); err != nil {
		return err
	}

	fmt.Printf("dry run: built %s %s\n\n", r.name, r.version)
	fmt.Printf("would publish to s3://%s/:\n", r.bucket)
	fmt.Printf("  %s\n", filepath.ToSlash(filepath.Join(namespace, r.name, r.version, "function.yaml")))
	fmt.Printf("  %s\n", filepath.ToSlash(filepath.Join(namespace, r.name, "release.txt")))
	fmt.Printf("  %s\n", filepath.ToSlash(filepath.Join(namespace, r.name, r.version, "checksums.txt")))
	for goos, archs := range r.platforms {
		for _, goarch := range archs {
			name := r.name
			if goos == "windows" {
				name += ".exe"
			}
			fmt.Printf("  %s\n", filepath.ToSlash(filepath.Join(namespace, r.name, r.version, goos, goarch, name)))
		}
	}
	return nil
}

func (r Releaser) build(ctx context.Context, buildDir string) error {
	c := compiler.New(compiler.Config{
		BaseDir:     buildDir,
		LDFlags:     "-s -w",
		Parallelism: runtime.NumCPU(),
	})
	defer c.Cleanup()

	for goos, archs := range r.platforms {
		for _, goarch := range archs {
			c.Add(compiler.Work{
				// Name maps to <buildDir>/<name>/<os>/<arch>/<name>, so the
				// releaser keys the upload as <name>/<version>/<os>/<arch>/<name>.
				Name:   filepath.Join(r.name, goos, goarch, r.name),
				Source: ".",
				Target: ".",
				Environment: []string{
					"GOOS=" + goos,
					"GOARCH=" + goarch,
				},
			})
		}
	}

	return c.Run(ctx)
}

// describe prefers a checked-in function.yaml at the repo root, else runs the
// native-platform build with `--describe` (kong's grammar doesn't vary by GOOS/GOARCH).
func (r Releaser) describe(ctx context.Context, buildDir string) ([]byte, error) {
	checkedIn := "function.yaml"
	data, err := os.ReadFile(checkedIn) //nolint:gosec // path is a fixed, repo-relative filename
	switch {
	case err == nil:
		return marshalDescriptor(data, r.version)
	case os.IsNotExist(err):
		bin := filepath.Join(buildDir, r.name, runtime.GOOS, runtime.GOARCH, r.name)
		out, err := exec.CommandContext(ctx, bin, "--describe").Output()
		if err != nil {
			return nil, fmt.Errorf("run %s --describe: %w", bin, err)
		}
		return marshalDescriptor(out, r.version)
	default:
		return nil, fmt.Errorf("read %s: %w", checkedIn, err)
	}
}

// marshalDescriptor parses, validates, and stamps the release version onto data.
func marshalDescriptor(data []byte, version string) ([]byte, error) {
	var d functionyaml.Descriptor
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse function.yaml: %w", err)
	}
	if err := functionyaml.Validate(d); err != nil {
		return nil, fmt.Errorf("invalid function.yaml: %w", err)
	}
	d.Version = version
	out, err := yaml.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal function.yaml: %w", err)
	}
	return out, nil
}

type cli struct {
	Name    string `required:"" help:"function name (e.g. setup-go)"`
	Version string `required:"" help:"version (e.g. v0.1.0-abc1234)"`
	Bucket  string `help:"S3 bucket to publish to (default: circleci-functions)"`
	DryRun  bool   `help:"build and write artifacts to ./dist without uploading to S3"`
}

func main() {
	var conf cli
	kong.Parse(&conf)

	ctx := context.Background()
	rel, err := newReleaser(ctx, Config{
		Name:    conf.Name,
		Version: conf.Version,
		Bucket:  conf.Bucket,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := rel.Run(ctx, conf.DryRun); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
