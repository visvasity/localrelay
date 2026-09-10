// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"

	"github.com/visvasity/httphelp"
	"github.com/visvasity/localrelay/servers"
)

// controlCall sends an RPC to the daemon's control socket in socketsDir, decoding
// the reply into resp.
func controlCall[REQ, RESP any](ctx context.Context, socketsDir, path string, req *REQ, resp *RESP) error {
	if socketsDir == "" {
		return errors.New("sockets directory could not be resolved (set -sockets-dir or XDG_RUNTIME_DIR)")
	}
	controlPath := filepath.Join(socketsDir, servers.ReservedName+".sock")
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", controlPath)
			},
		},
	}
	return httphelp.CallPostHandler(ctx, "http://unix"+path, req, resp, client)
}
