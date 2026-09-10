// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
)

// RemoveCmd deletes an explicit relay (or a per-user delegation with -user) from
// the running daemon over its control socket. It is the inverse of AddCmd.
type RemoveCmd struct {
	socketsDir string
	user       bool
}

func (c *RemoveCmd) Purpose() string {
	return "Remove a relay from the daemon"
}

func (c *RemoveCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.StringVar(&c.socketsDir, "sockets-dir", "", "Path to the reverse proxy target sockets directory")
	fset.BoolVar(&c.user, "user", false, "Remove a per-user subdomain delegation: <username>")
	return "remove", fset, c.run
}

func (c *RemoveCmd) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		if c.user {
			return errors.New("usage: remove -user <username>")
		}
		return errors.New("usage: remove <name>")
	}
	socketsDir := servers.ResolveSocketsDir(c.socketsDir)

	req := servers.RemoveRequest{Name: args[0], User: c.user}
	var resp servers.RemoveResponse
	if err := controlCall(ctx, socketsDir, servers.RemovePath, &req, &resp); err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}
	return nil
}
