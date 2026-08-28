// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"net/http/pprof"
	_ "net/http/pprof"

	"github.com/visvasity/cli"
	"github.com/visvasity/httphelp"
	"github.com/visvasity/logdir"
	"github.com/visvasity/runcmd"
)

// RunCmd is a sub-command that uses demonstrates how to use the
// background/self-monitor/restart features implemented by the runcmd package.
type RunCmd struct {
	runcmd.RunFlags
	httphelp.ServerFlags
	httphelp.ClientFlags

	pprofPort int

	dbPort int

	dataDir string
}

func (c *RunCmd) Purpose() string {
	return "FIXME: Describe the purpose of the daemon program"
}

func (c *RunCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	c.RunFlags.SetFlags(fset, &c.RunFlags)
	c.ServerFlags.SetFlags(fset, &c.ServerFlags)
	c.ClientFlags.SetFlags(fset, &c.ClientFlags)
	fset.IntVar(&c.pprofPort, "pprof-port", 6060, "Pprof access port on localhost/127.0.0.1 for net/http/pprof information")
	return "run", fset, c.RunFlags.WithRunFunc(c.run)
}

func (c *RunCmd) Check(ctx context.Context) error {
	if err := c.ServerFlags.Check(ctx); err != nil {
		return err
	}
	if err := c.ClientFlags.Check(ctx); err != nil {
		return err
	}
	if c.pprofPort < 0 {
		return fmt.Errorf("pprof-port %d is invalid", c.pprofPort)
	}
	if c.dbPort < 0 {
		return fmt.Errorf("db-port %d is invalid", c.dbPort)
	}
	if c.dbPort == c.pprofPort {
		return fmt.Errorf("db and pprof ports cannot be the same")
	}
	return nil
}

func (c *RunCmd) run(ctx context.Context, args []string) (status error) {
	if err := c.Check(ctx); err != nil {
		return err
	}

	sink, err := logdir.Open(logdir.Config{Dir: "/tmp"})
	if err != nil {
		return err
	}
	defer sink.Close()

	slog.SetDefault(sink.Logger(""))

	http.DefaultClient = c.ClientFlags.Client()

	httpServer, err := httphelp.NewServer()
	if err != nil {
		return err
	}
	defer httpServer.Close()

	// Provide pprof access over HTTP. Pprof access is provided only on localhost.
	if c.pprofPort != 0 {
		host := "localhost"
		if c.pprofPort != 80 {
			host = net.JoinHostPort(host, strconv.Itoa(c.pprofPort))
		}
		addr, err := httpServer.Start(host)
		if err != nil {
			return err
		}
		defer httpServer.Stop(addr)

		slog.Info("starting pprof server at path /debug/pprof/ on localhost and 127.0.0.1")
		httpServer.Handle("localhost/debug/pprof/", http.HandlerFunc(pprof.Index))
		httpServer.Handle("127.0.0.1/debug/pprof/", http.HandlerFunc(pprof.Index))
	}

	// When command is invoked with -background flag, reporting initialization
	// status to the foreground process informs user of success or failure.
	runcmd.Report(ctx, nil)

	for ctx.Err() == nil {
		time.Sleep(time.Second)
	}

	return nil
}
