// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
)

// AddCmd registers an explicit relay <name> -> <target> with the running daemon
// over its control socket.
type AddCmd struct {
	socketsDir string
	user       bool
	cleanURL   bool
}

func (c *AddCmd) Purpose() string {
	return "Register an explicit relay with the daemon"
}

func (c *AddCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.StringVar(&c.socketsDir, "sockets-dir", "", "Path to the reverse proxy target sockets directory")
	fset.BoolVar(&c.user, "user", false, "Register a per-user subdomain delegation: <username> <uds-socket>")
	fset.BoolVar(&c.cleanURL, "clean-url", false, "Rewrite the forwarded Host to the backend host (127.0.0.1 for tcp, unix for unix) instead of <name>.localhost")
	return "add", fset, c.run
}

func (c *AddCmd) run(ctx context.Context, args []string) error {
	if len(args) != 2 {
		if c.user {
			return errors.New("usage: add -user <username> <uds-socket>")
		}
		return errors.New("usage: add <name> <target>")
	}
	socketsDir := servers.ResolveSocketsDir(c.socketsDir)

	req := servers.AddRequest{Name: args[0], Target: args[1], User: c.user, CleanURL: c.cleanURL}
	var resp servers.AddResponse
	if err := controlCall(ctx, socketsDir, servers.AddPath, &req, &resp); err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	return nil
}
