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

	cmds := []cli.Command{
		runCmd,
	}
	if err := cli.Run(context.Background(), cmds, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
