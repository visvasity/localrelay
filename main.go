// Copyright (c) 2026 Visvasity LLC

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/subcmds"
	"github.com/visvasity/runcmd"
)

func main() {
	caCmds := []cli.Command{
		new(subcmds.CAInitCmd),
		new(subcmds.CAInstallCmd),
		new(subcmds.CAUninstallCmd),
	}

	cmds := []cli.Command{
		runcmd.Wrap(new(subcmds.Serve)),
		new(subcmds.AddCmd),
		new(subcmds.RemoveCmd),
		new(subcmds.ListCmd),
		cli.NewGroup("ca", "Certificate authority operations", caCmds...),
	}
	if err := cli.Run(context.Background(), cmds, os.Args[1:]); err != nil {
		slog.Error("failed", "err", err)
		os.Exit(1)
	}
}
