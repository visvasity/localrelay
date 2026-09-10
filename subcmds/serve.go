// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
	"github.com/visvasity/logdir"
	"github.com/visvasity/runcmd"
)

// Serve runs the localrelay per-user daemon, reusing runcmd for the
// background/self-monitor/restart lifecycle flags.
type Serve struct {
	servers.Options

	dataDir string
}

func (c *Serve) Purpose() string {
	return "Run the localrelay per-user daemon"
}

func (c *Serve) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	c.Options.SetFlags(fset, nil)
	fset.StringVar(&c.dataDir, "data-dir", "", "Path to the data directory")
	return "run", fset, c.run
}

func (c *Serve) Check(ctx context.Context) error {
	c.dataDir = servers.ResolveDataDir(c.dataDir)
	if err := c.Options.Check(ctx); err != nil {
		return err
	}
	return nil
}

// LocksDir method will cause runcmd to create daemonizing lock files in this directory.
func (c *Serve) LocksDir() string {
	return c.dataDir
}

func (c *Serve) run(ctx context.Context, args []string) error {
	logsDir := filepath.Join(c.dataDir, "logs")
	if os.Getuid() == 0 {
		logsDir = "/var/log"
	}
	sink, err := logdir.Open(logdir.Config{Dir: logsDir})
	if err != nil {
		return err
	}
	defer sink.Close()

	slog.SetDefault(sink.Logger(""))

	// server is the daemon lifecycle interface.
	type server interface {
		Start(context.Context) error
		Stop() error
		Close() error
	}
	var srv server

	srv, err = servers.New(c.dataDir, &c.Options)
	if err != nil {
		return err
	}
	defer srv.Close()

	if err := srv.Start(ctx); err != nil {
		return err
	}
	defer srv.Stop()

	// Signal successful initialization to the foreground/monitor process.
	runcmd.Report(ctx, nil)

	<-ctx.Done()
	return context.Cause(ctx)
}
