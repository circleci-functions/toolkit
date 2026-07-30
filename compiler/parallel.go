/*
Package compiler aids with compiling your Go binaries efficiently and in a consistent way.
*/
package compiler

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Config is the configuration for the compiler's constructor.
type Config struct {
	BaseDir     string
	LDFlags     string
	Parallelism int
}

// Parallel allows you to add various compiler work, and will run it all in parallel, reporting back compilation errors.
type Parallel struct {
	compiler    *compiler
	parallelism int

	work chan Work
}

// New is the constructor for Parallel
func New(cfg Config) *Parallel {
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 2
	}

	return &Parallel{
		compiler:    newCompiler(cfg.BaseDir, cfg.LDFlags),
		parallelism: cfg.Parallelism,
		work:        make(chan Work, 100),
	}
}

// Dir returns the directory the compiler is compiling into.
func (t *Parallel) Dir() string {
	return t.compiler.Dir()
}

// Cleanup informs the compiler that no more work will be added.
func (t *Parallel) Cleanup() {
	close(t.work)
}

// Add work to the parallel compiler.
func (t *Parallel) Add(work Work) {
	if work.Name == "" {
		panic("work.Name not set")
	}
	if work.Target == "" {
		panic("work.Target not set")
	}
	if work.Source == "" {
		panic("work.Source not set")
	}

	if work.Result == nil || *work.Result == "" {
		t.work <- work
	}
}

// Run the actual compilation, reporting any errors.
func (t *Parallel) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for range t.parallelism {
		g.Go(func() error {
			for {
				select {
				case w := <-t.work:
					_, err := t.compiler.Compile(ctx, w)
					if err != nil {
						return err
					}
				default:
					return nil
				}
			}
		})
	}
	return g.Wait()
}
