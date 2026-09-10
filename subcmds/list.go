// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
)

// ListCmd prints the relays and user delegations registered with the daemon.
type ListCmd struct {
	socketsDir string
}

func (c *ListCmd) Purpose() string {
	return "List registered relays and user delegations"
}

func (c *ListCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.StringVar(&c.socketsDir, "sockets-dir", "", "Path to the reverse proxy target sockets directory")
	return "list", fset, c.run
}

func (c *ListCmd) run(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: list")
	}
	socketsDir := servers.ResolveSocketsDir(c.socketsDir)

	var req servers.ListRequest
	var resp servers.ListResponse
	if err := controlCall(ctx, socketsDir, servers.ListPath, &req, &resp); err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}

	w := tabwriter.NewWriter(cli.Stdout(ctx), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tTARGET\tCLEAN-URL\tSTATUS")
	for _, e := range resp.Entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", e.Name, e.Kind, e.Target, e.CleanURL, e.Status)
	}
	return w.Flush()
}
