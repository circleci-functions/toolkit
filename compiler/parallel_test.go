package compiler

import (
	"context"
	"os"
	"path"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/fs"
	"gotest.tools/v3/golden"
	"gotest.tools/v3/icmd"
)

func TestParallel_Compile(t *testing.T) {
	tmpDir := fs.NewDir(t, "")
	c := New(Config{
		BaseDir:     tmpDir.Path(),
		LDFlags:     "-w -s",
		Parallelism: 2,
	})

	var binary1, binary2, binary3 string
	t.Cleanup(func() {
		c.Cleanup()
	})

	assert.Assert(t, t.Run("Compile binaries", func(t *testing.T) {
		c.Add(Work{
			Result:      &binary1,
			Name:        "binary1",
			Target:      "..",
			Source:      "./compiler/internal/cmd",
			Environment: []string{"FOO=foo1", "BAR=bar1"},
		})
		c.Add(Work{
			Result:      &binary2,
			Name:        "binary2",
			Target:      "..",
			Source:      "./compiler/internal/cmd2",
			Environment: []string{"FOO=foo2", "BAR=bar2"},
			Tags:        "work",
		})
		c.Add(Work{
			Result:      &binary3,
			Name:        "binary3",
			Target:      "..",
			Source:      "./compiler/internal/cmd3",
			Environment: []string{"FOO=foo2", "BAR=bar2"},
			Tags:        "work",
			ExtraArgs:   []string{"-cover"},
		})

		err := c.Run(context.Background())
		assert.Check(t, err)

		_, err = os.Stat(binary1)
		assert.Check(t, err)

		_, err = os.Stat(binary2)
		assert.Check(t, err)

		_, err = os.Stat(binary3)
		assert.Check(t, err)
	}))

	t.Run("Run binaries", func(t *testing.T) {
		res := icmd.RunCommand(binary1, "arg1", "arg2", "arg3")
		assert.Check(t, res.Equal(icmd.Expected{
			Out: "command 1: [arg1 arg2 arg3]",
		}))

		res = icmd.RunCommand(binary2, "arg1", "arg2", "arg3")
		assert.Check(t, res.Equal(icmd.Expected{
			Out: "command 2: [arg1 arg2 arg3] correct",
		}))
	})

	t.Run("Run binary with coverage", func(t *testing.T) {
		// N.B. we use the txt extension here to not fall foul of the .out in .gitignore
		// we would expect instrumented binary runs to use .o or .out for coverage report extensions.
		dir := fs.NewDir(t, "compiler-test")
		t.Setenv("GOCOVERDIR", dir.Path())

		res := icmd.RunCommand(binary3, "arg1", "arg2", "arg3")
		assert.Check(t, res.Equal(icmd.Expected{
			Out: "command 3: [arg1 arg2 arg3] correct",
		}))

		// run the go covdata toot to create the old coverage
		covFile := path.Join(dir.Path(), "coverage.txt")

		res = icmd.RunCommand("go",
			"tool", "covdata", "textfmt", "-i="+dir.Path(), "-o", covFile)
		assert.Check(t, cmp.Equal(res.ExitCode, 0))

		assert.Check(t, golden.String(string(golden.Get(t, covFile)), "coverage.txt"))

		res = icmd.RunCommand(binary2, "arg1", "arg2", "arg3")
		assert.Check(t, res.Equal(icmd.Expected{
			Out: "command 2: [arg1 arg2 arg3]",
		}))
	})
}
