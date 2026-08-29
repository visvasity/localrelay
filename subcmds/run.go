// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
	"github.com/visvasity/runcmd"
)

// RunCmd runs the localrelay per-user daemon, reusing runcmd for the
// background/self-monitor/restart lifecycle flags.
type RunCmd struct {
	runcmd.RunFlags
	servers.Options

	dataDir string
}

func (c *RunCmd) Purpose() string {
	return "Run the localrelay per-user daemon"
}

func (c *RunCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	c.RunFlags.SetFlags(fset, &c.RunFlags)
	c.Options.SetFlags(fset, &servers.Options{
		HTTPPort:  1080,
		HTTPSPort: 1443,
	})
	fset.StringVar(&c.dataDir, "data-dir", "", "Path to the data directory")
	return "run", fset, c.RunFlags.WithRunFunc(c.run)
}

func (c *RunCmd) Check(ctx context.Context) error {
	if len(c.dataDir) == 0 {
		return fmt.Errorf("data directory (-data-dir) is required")
	}
	return nil
}

func (c *RunCmd) run(ctx context.Context, args []string) error {
	if err := c.Check(ctx); err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	user, err := servers.New(c.dataDir, &c.Options)
	if err != nil {
		return err
	}
	defer user.Close()

	if err := user.Start(ctx); err != nil {
		return err
	}
	defer user.Stop()

	// Signal successful initialization to the foreground/monitor process.
	runcmd.Report(ctx, nil)

	<-ctx.Done()
	return context.Cause(ctx)
}
