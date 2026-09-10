// Copyright (c) 2026 Visvasity LLC

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/subcmds"
	"github.com/visvasity/runcmd"
)

func main() {
	runCmd := &subcmds.RunCmd{
		RunFlags: runcmd.RunFlags{
			LocksDir: filepath.Join(os.Getenv("HOME"), ".localrelay"),
		},
	}
	caCmds := []cli.Command{
		new(subcmds.CAInitCmd),
		new(subcmds.CAInstallCmd),
		new(subcmds.CAUninstallCmd),
	}

	cmds := []cli.Command{
		runCmd,
		cli.NewGroup("ca", "Certificate authority operations", caCmds...),
	}
	if err := cli.Run(context.Background(), cmds, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
