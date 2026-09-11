// Copyright (c) 2026 Visvasity LLC

package servers

import "fmt"

// ReservedName is the reserved service name: never routable, not registrable as a
// relay, and the basename of the control socket (control.sock).
const ReservedName = "control"

// ValidName reports whether name is a valid service / relay label: 1..63 bytes of
// lowercase a-z, 0-9 and '-', not starting or ending with '-'.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("name %q exceeds the 63-character label limit", name)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			// alphanumerics valid anywhere
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("name %q must not start or end with a hyphen", name)
			}
		default:
			return fmt.Errorf("name %q contains invalid character %q (allowed: a-z, 0-9, '-')", name, c)
		}
	}
	return nil
}

// AddPath is the control-channel endpoint for the add verb.
const AddPath = "/api/add"

// AddRequest registers an explicit relay <Name> -> <Target> with the daemon.
// Target is an http/https URL: the scheme selects the backend hop's protocol
// (plaintext vs TLS) and host-vs-path selects tcp vs unix, e.g.
// "http://127.0.0.1:8080", "https://127.0.0.1:8443", or "http:///run/app.sock".
//
// When User is true the request instead registers a per-user subdomain
// delegation: Name is a username and Target is the absolute path to a Unix
// socket owned by that user. Every <*>.<Name>.localhost request is forwarded to
// the socket, whose ownership is verified against the user at dispatch time.
type AddRequest struct {
	Name   string
	Target string
	User   bool

	// CleanURL, when true, makes the relay rewrite the forwarded Host to the
	// literal backend host ("127.0.0.1" for a tcp target, "unix" for a unix
	// target) instead of the <name>.localhost form.
	CleanURL bool
}

// AddResponse is the reply for AddRequest. A failure is reported in Err rather
// than as a handler error: httphelp encodes a returned error as a different gob
// type, which would decode as a zero-value success on the client.
type AddResponse struct {
	Err string
}

// RemovePath is the control-channel endpoint for the remove verb.
const RemovePath = "/api/remove"

// RemoveRequest removes an explicit relay named Name, or a per-user delegation
// when User is set. It is the inverse of AddRequest.
type RemoveRequest struct {
	Name string
	User bool
}

// RemoveResponse is the reply for RemoveRequest; failures travel in Err for the
// same reason as AddResponse.
type RemoveResponse struct {
	Err string
}

// ListPath is the control-channel endpoint for the list verb.
const ListPath = "/api/list"

// ListRequest asks the daemon for all registered relays and user delegations.
type ListRequest struct{}

// Relay kinds reported by ListEntry.Kind.
const (
	// KindRelay is an explicit relay added via the add subcommand.
	KindRelay = "relay"
	// KindUser is a per-user delegation (<*>.<Name>.localhost -> unix socket).
	KindUser = "user"
	// KindSocket is an automatic relay for a <Name>.sock in the sockets
	// directory; it is not persisted and carries no clean-url setting.
	KindSocket = "socket"
)

// Relay liveness states reported by ListEntry.Status, from periodic connect
// probes of the target.
const (
	StatusUp      = "up"
	StatusDown    = "down"
	StatusUnknown = "unknown"
)

// ListEntry describes one active relay. Kind is one of KindRelay, KindUser, or
// KindSocket; Status is one of StatusUp, StatusDown, or StatusUnknown.
type ListEntry struct {
	Name     string
	Target   string
	Kind     string
	CleanURL bool
	Status   string
}

// ListResponse is the reply for ListRequest, sorted by name. Failures travel in
// Err for the same reason as AddResponse.
type ListResponse struct {
	Entries []ListEntry
	Err     string
}
