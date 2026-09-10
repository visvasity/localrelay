// Copyright (c) 2026 Visvasity LLC

package main

import (
	"context"
	"log"
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
		cli.NewGroup("ca", "Certificate authority operations", caCmds...),
	}
	if err := cli.Run(context.Background(), cmds, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
