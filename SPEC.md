# localrelay — Protocol and Behavior Specification

**Version:** 1.0
**Status:** Normative (v1, initial implementation target)
**Applies to:** the `localrelay` binary operating as a per-user daemon, a system daemon, and a command-line tool.

---

## 1. Introduction and Scope

`localrelay` is a reverse HTTP proxy. It accepts HTTP requests addressed to
`<name>.localhost` and forwards them to a local backend. A single binary
provides the background daemon and the command-line interface; the daemon is
started with the `run` subcommand, and all other subcommands are client-side
operations.

The daemon operates in one of two modes:

- **Per-user mode** (the default) — an unprivileged daemon serving a single
  user. A backend is reached either by **conventional discovery** (a service
  creates a Unix domain socket named for itself in the sockets directory; its
  presence is registration) or by an **explicit relay** (a name registered
  through the CLI against a loopback TCP address or an arbitrary Unix socket
  path, held in a persistent registry and managed over the control channel).
- **System mode** (`run --system`) — a privileged daemon whose sole
  function is **user delegation**: it routes a whole subtree `*.<user>.localhost`
  to that user's per-user daemon over a Unix socket the user owns. The system
  daemon serves only the delegation verbs; it MUST NOT serve the relay API.

This document specifies the observable behavior an implementation MUST exhibit.
It is written to be sufficient for a clean-room implementation.

### 1.1 In scope for version 1

- A per-user daemon serving plain HTTP (unprivileged; the default mode).
- Conventional discovery of backends over Unix domain sockets, by lazy
  dial-on-demand with no registry and no discovery subsystem.
- Explicit, CLI-managed relays to IPv4-loopback TCP ports and to Unix sockets at
  arbitrary paths, persisted across daemon restarts by default.
- A control channel (Unix domain socket) through which the CLI manages explicit
  relays.
- A system daemon (privileged) providing user delegation of
  `*.<user>.localhost` subtrees to per-user daemons, where each delegation
  target is a Unix socket owned by the delegated user, authorized by peer
  credentials (§12).
- Exactly one backend per service name, and exactly one delegation per user.
- A loopback TCP listener.
- Optional HTTPS: the per-user daemon terminates TLS from an operator-supplied
  certificate and key, and the system daemon terminates TLS under an
  operator-supplied name-constrained CA, minting per-user leaves on demand (§13).
- Linux as the primary platform, with macOS as a compatible secondary target.

### 1.2 Explicitly deferred (see §17)

IPv6 targets and listeners, arbitrary subtree delegation to arbitrary
(non-user-owned) targets, and an optional local DNS responder. An
implementation of version 1 MUST NOT rely on any of these being present, but
MUST honor the forward-compatibility reservations in §16.

---

## 2. Conventions and Terminology

### 2.1 Requirement keywords

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **MAY**, and **OPTIONAL** in this document are to be
interpreted as described in RFC 2119 and RFC 8174 when, and only when, they
appear in all capitals.

### 2.2 Terms

- **Daemon** — a running instance of `localrelay run` (per-user mode) or
  `localrelay run --system` (system mode).
- **Client** — the HTTP user agent (typically a browser) connecting to a
  daemon's listener.
- **Service** — a backend process that accepts HTTP over a Unix domain socket or
  a loopback TCP port.
- **Service name** — the single DNS label identifying a service, e.g. `foo`.
- **Conventional socket** — a Unix domain socket named `<name>.sock` in the
  sockets directory, discovered by convention (§4, §6).
- **Explicit relay** — a name-to-target mapping registered through the CLI and
  held in the relay registry (§11).
- **Target** — the address a relay or delegation forwards to.
- **Delegation** — a system-mode mapping from a user's subtree
  `*.<user>.localhost` to a Unix socket owned by that user (§12).
- **Sockets directory** — the directory holding conventional service sockets and
  the control socket (§4; per-mode locations in §4.1 and §12.2).
- **Data directory** — the directory holding persistent daemon state (the relay
  registry in per-user mode, the delegation registry in system mode).
- **Control socket** — the Unix domain socket at which a daemon serves its
  control channel.

---

## 3. Architecture Overview

In per-user mode, a client sends an HTTP request to the daemon's loopback
listener with a Host header `<name>.localhost`. The daemon resolves the name to
a backend by the precedence rule in §6.3 — an explicit relay if registered,
otherwise the conventional socket `<name>.sock` — then dials the backend and
proxies the exchange. Conventional discovery is stateless and lazy; explicit
relays are deliberate configuration held in a persistent registry and managed
over the control channel (§11).

In system mode, the daemon owns the privileged port and routes three-label
hosts `<name>.<user>.localhost` to the delegated user's per-user daemon. It
strips the user label, rewrites the Host to `<name>.localhost`, and forwards over
a Unix socket the user owns (§12). The per-user daemon therefore never sees a
three-label host and behaves identically whether standalone or behind a system
daemon. All multi-label routing, and its authorization, live in the system
daemon alone.

---

## 4. Sockets Directory and Socket Layout

This section applies to per-user mode. System-mode paths are specified in §12.2.

### 4.1 Sockets directory resolution

The per-user daemon and CLI MUST resolve the sockets directory as follows, in
order:

1. If the `-sockets-dir` flag is provided, its value is the sockets directory.
2. Otherwise, if the `XDG_RUNTIME_DIR` environment variable is set and
   non-empty, the sockets directory is `$XDG_RUNTIME_DIR/localrelay`.
3. Otherwise, the operation MUST fail with a clear diagnostic instructing the
   user to set `XDG_RUNTIME_DIR` or pass `-sockets-dir`.

The per-user daemon MUST ensure the sockets directory exists, creating it with
mode `0700` if absent. If the directory exists with broader permissions than
`0700`, the daemon SHOULD emit a warning; it MAY refuse to start.

The sockets directory MUST be a subdirectory dedicated to `localrelay` (default
`localrelay/` under `XDG_RUNTIME_DIR`) and MUST NOT be the bare
`XDG_RUNTIME_DIR`, because that directory is shared with other applications and
a bare layout would make socket discovery ambiguous.

### 4.2 Conventional socket path derivation

For a service named `<name>`, the conventional socket path is:

```
<sockets-dir>/<name>.sock
```

Because a service name is a single DNS label (§5) it contains no path separator
and no `.` or `..` component; therefore the derived socket path is always a
single file directly within the sockets directory. The daemon MUST NOT dial any
conventional path outside the sockets directory.

---

## 5. Service Names

### 5.1 Grammar

A service name MUST be a single lowercase DNS label:

```abnf
service-name = alnum *( alnum / "-" ) alnum
             / alnum
alnum        = %x61-7A / %x30-39   ; a-z 0-9
```

That is: one or more characters drawn from lowercase ASCII letters, ASCII
digits, and the hyphen; the first and last characters MUST NOT be a hyphen.
Uppercase letters, underscores, dots, and any other character are invalid. This
grammar applies to conventional socket names, explicit relay names, and the user
label of a delegation (§12.4).

### 5.2 Length

The daemon and CLI MUST reject a service name if the resulting conventional
socket path (§4.2), encoded as bytes, would meet or exceed the platform's
`sun_path` limit (108 bytes on Linux, 104 bytes on macOS). Because the sockets
directory prefix varies, the maximum service-name length is not a fixed constant
and MUST be computed from the resolved sockets directory. The same bound applies
to explicit relay names.

### 5.3 Reserved names

The service name `control` is RESERVED (see §16). The daemon MUST NOT route a
request for `control.localhost` to a backend, and MUST instead return `404`
(§8), regardless of whether a socket by that name exists. The CLI MUST reject an
attempt to register an explicit relay named `control` (§11.3).

---

## 6. Host Resolution and Routing (Per-User Mode)

This section specifies routing for a per-user daemon. System-mode routing is
specified in §12.4.

### 6.1 Host header normalization

For each request the daemon MUST derive a candidate host string from the request
by the following steps:

1. Take the `Host` header field value. For HTTP/1.1 an absent or empty Host
   MUST result in `404` (§8).
2. If the value contains a port (`host:port`), remove the port.
3. Remove a single trailing dot if present.
4. Lowercase the result using ASCII case folding only.

### 6.2 Match rule

The normalized host MUST match exactly two labels where the final label is
`localhost`:

```abnf
routed-host  = service-name "." "localhost"
```

If the normalized host is not of this form — including bare `localhost` or any
non-`localhost` host — the daemon MUST return `404` (§8). A per-user daemon MUST
return `404` for any multi-label host such as `<name>.<user>.localhost`; such
hosts are handled only by a system daemon (§12.4) and MUST NOT be routed by a
per-user daemon.

If the extracted service name is reserved (§5.3), the daemon MUST return `404`.

### 6.3 Resolution precedence and routing

Given a valid, non-reserved service name, the daemon MUST resolve the backend in
the following order:

1. **Explicit relay.** If the relay registry (§11) contains an entry for the
   name, the target is that entry's target (a loopback TCP address or a Unix
   socket path).
2. **Conventional socket.** Otherwise, the target is the conventional socket
   path `<sockets-dir>/<name>.sock` (§4.2).

Having selected a target, the daemon MUST:

3. Dial the target, subject to a bounded connect timeout (default 5 seconds; see
   Appendix B).
4. If the dial fails for any reason (socket or port absent, connection refused,
   timeout, permission denied), return `502` (§8).
5. If the dial succeeds, proxy the request per §7.

The daemon MUST perform this resolution per request. Conventional resolution
MUST NOT require a directory scan, filesystem watch, or in-memory registry. An
implementation MAY pool or cache backend connections as an optimization,
provided the observable behavior is unchanged.

---

## 7. Request Forwarding Semantics

### 7.1 General

The daemon acts as an HTTP/1.1 reverse proxy. It MUST speak HTTP/1.1 to the
backend (over either the Unix domain socket or the loopback TCP connection). It
MUST accept HTTP/1.0 and HTTP/1.1 from the client. Forwarding semantics are
identical regardless of how the backend was reached (conventional socket,
explicit relay, or a delegation target in system mode) and regardless of the
target kind.

The request method, request target (path and query), and message body MUST be
forwarded unchanged. The daemon MUST NOT alter the response body.

### 7.2 Hop-by-hop headers

The daemon MUST remove hop-by-hop header fields before forwarding in each
direction, as required by RFC 9110/9112: `Connection` (and any field named
therein), `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`,
`Trailer`, `Transfer-Encoding`, and `Upgrade`, except where an upgrade is being
performed under §7.4.

### 7.3 Forwarded headers

The daemon MUST set the following request headers seen by the backend:

- `Host`: preserved as `<name>.localhost`. In system mode this is the rewritten
  two-label host after the user label is stripped (§12.4).
- `X-Forwarded-Host`: set authoritatively, overwriting any client-supplied
  value. In system mode it carries the original three-label host (§12.4).
- `X-Forwarded-Proto`: set to the client-facing scheme — `https` when the client
  connection to the terminating daemon used TLS, otherwise `http` — overwriting
  any client-supplied value (§13).
- `X-Forwarded-For`: the immediate client address appended per RFC 7239
  conventions. The daemon SHOULD NOT trust a client-supplied `X-Forwarded-For`
  as authoritative but MAY append to it.

### 7.4 Connection upgrade and WebSocket

The daemon MUST support HTTP connection upgrades. When a request carries
`Connection: Upgrade` and the backend responds with `101 Switching Protocols`,
the daemon MUST establish a transparent bidirectional tunnel between client and
backend for the lifetime of the upgraded connection. This covers WebSocket.

### 7.5 Streaming, timeouts, and buffering

The daemon MUST stream request and response bodies rather than fully buffering
them, and MUST NOT impose a maximum body size of its own. The daemon MUST NOT
impose a total response deadline that would terminate long-lived streaming
responses or upgraded connections; only the backend connect step is
time-bounded in version 1.

### 7.6 Trailers

The daemon SHOULD forward HTTP trailers when present.

---

## 8. Error Responses

When the daemon cannot forward a request it MUST generate a response itself,
with a `Content-Type` of `text/plain; charset=utf-8` and a short single-line
body. The following statuses are defined:

- **404 Not Found** — the request host is not a routable host for the daemon's
  mode (§6.2, §12.4), or the service name is reserved (§5.3). The body SHOULD
  identify that the host is not a routable `*.localhost` service.
- **502 Bad Gateway** — the host was valid but the selected backend could not be
  reached, or the backend connection failed before any response status line was
  received. In system mode this also covers a delegation whose target fails the
  ownership check (§12.5). The body SHOULD indicate that no reachable service
  answered for the given name.

Once the backend's response status line has been forwarded to the client, a
subsequent mid-stream backend failure MUST NOT change the status code; the
daemon MUST terminate the client connection instead.

---

## 9. Daemon: the `run` Subcommand

### 9.1 Invocation

```
localrelay run [-addr <addr>] [-sockets-dir <dir>] [-data-dir <dir>] [-tls-crt <file>] [-tls-key <file>] [-background]
localrelay run --system [-addr <addr>] [-sockets-dir <dir>] [-data-dir <dir>] [-tls-ca-crt <file>] [-tls-ca-key <file>] [-background]
```

Without `--system` the daemon runs in per-user mode, specified in §§4–11 and §13. With
`--system` it runs in system mode, specified in §12 and §13.6; the per-user behavior in
this section applies except where §12 overrides it (privileged listener, system
paths, delegation-only routing and control API). The `-tls-crt` and `-tls-key`
flags enable HTTPS in per-user mode (§13). In system mode, HTTPS is instead
enabled by the CA flags `-tls-ca-crt`/`-tls-ca-key` (§13.6); the per-user leaf
flags `-tls-crt`/`-tls-key` MUST be rejected in system mode.

### 9.2 Listener

The daemon MUST bind a TCP listener on the address given by `-addr`. In per-user
mode the default is `127.0.0.1:7900`; the default address MUST be a loopback
address and, because per-user mode targets non-privileged operation, the default
port MUST be non-privileged (>= 1024). The system-mode default is specified in
§12.2.

When `-tls-crt` and `-tls-key` are both supplied (per-user mode), the listener
serves HTTPS on `-addr` instead of plain HTTP; the port is unchanged, so a
non-privileged default such as `127.0.0.1:7900` is reached as
`https://<name>.localhost:7900/` (§13). A dedicated TLS listener and an
HTTP-to-HTTPS redirect are not provided in version 1.

Binding the listener address serves as the single-instance interlock: a second
daemon started with the same `-addr` will fail to bind and MUST exit with a
diagnostic. No separate pidfile or lock file is required in version 1.

### 9.3 Data directory and startup sequence

The daemon and CLI MUST resolve the data directory from the `-data-dir` flag if
given, otherwise (per-user mode) `$XDG_STATE_HOME/localrelay`, otherwise
`$HOME/.local/state/localrelay`. The system-mode default is specified in §12.2.
The data directory holds persistent daemon state and MUST be distinct from the
sockets directory, because it MUST survive a reboot whereas the sockets
directory is runtime-scoped. The daemon MUST create the data directory with mode
`0700` if absent.

On startup the daemon MUST, in order: resolve the sockets directory and the data
directory; ensure both exist with the required permissions; load the persisted
registry (the relay registry in per-user mode, §11.5; the delegation registry in
system mode, §12.6); validate any configured TLS material (§13); bind the control
socket; bind the listener (§9.2); and only then begin accepting connections. If any step fails the daemon MUST exit non-zero
with a diagnostic and MUST NOT leave a partially initialized listener accepting
traffic.

### 9.4 Foreground and background

By default the daemon runs in the foreground, attached to the controlling
terminal, logging to standard error. With `-background` the daemon MUST detach
from the controlling terminal and continue running in the background; in that
mode it MUST redirect its logging away from the now-detached terminal. The
mechanism of detachment is unspecified; only the observable behavior is required.

### 9.5 Shutdown

On `SIGINT` or `SIGTERM` the daemon MUST perform a graceful shutdown: stop
accepting new connections, allow in-flight exchanges a bounded grace period to
complete, then exit. The persisted registry is written as changes occur, so no
shutdown-time flush is required.

`SIGHUP` has no configuration file to reload in version 1; the daemon MUST treat
it as a no-op or MAY use it to reopen its log file.

### 9.6 Logging

The daemon SHOULD emit a per-request access log line including at least the
request method, the requested host, the resulting status code, and the service
duration. Access logging MUST NOT include request or response bodies.

---

## 10. Command-Line Interface

The binary uses a subcommand structure: `localrelay <subcommand> [flags]`.
Version 1 defines: `run`, `add`, `remove`, `get`, `list`, `version` (per-user
control API), and `add-user`, `remove-user`, `list-users` (system delegation
API, §12.7). A subcommand's verb selects which control socket it addresses: the
relay verbs address the per-user control socket; the `*-user` verbs address the
system control socket. Except for `run` and `version`, these subcommands contact
a daemon over its control socket and fail if it is unreachable; `list` is the
sole exception, degrading gracefully to report conventional sockets even when the
per-user daemon is not running (§10.2).

### 10.1 `run`

Starts a daemon. Specified in §9 (and §12 for `--system`).

### 10.2 `list`

Lists the backends a per-user daemon can currently route, combining both
mechanisms. It MUST report explicit relays (from the daemon over the control
channel: name, kind `tcp`/`unix`, target) and conventional sockets (by scanning
the sockets directory for `<name>.sock`, excluding the reserved `control.sock`
and any name already shown as an explicit relay, per §6.3). Output is sorted by
name; `--json` selects a machine-readable form. `list` reports presence, not
liveness, and MUST NOT dial backends by default.

If the control channel is unavailable, `list` MUST still report conventional
sockets, MUST indicate that explicit relays could not be retrieved, and MUST exit
non-zero to signal a partial result.

### 10.3 `add`

```
localrelay add <name> <target> [--force] [--ephemeral]
```

Registers an explicit relay mapping `<name>` to `<target>` (§11). The CLI MUST
validate `<name>` against §5 and reject `control`. It MUST validate and normalize
`<target>` per §11.2, rejecting non-loopback and IPv6 targets. If a relay already
exists for `<name>`, `add` MUST fail unless `--force` is given. `--ephemeral`
registers without persisting (§11.5). If a conventional socket of the same name
exists, `add` MUST still succeed and SHOULD warn that the relay takes precedence
(§6.3).

### 10.4 `remove`

```
localrelay remove <name>
```

Removes the explicit relay for `<name>`, failing if none is registered. It MUST
NOT delete a conventional socket, which is owned by the service. There is no `rm`
alias.

### 10.5 `get`

```
localrelay get <name>
```

Prints how `<name>` resolves: the explicit relay's kind and target if
registered; otherwise the conventional socket path if the file exists; otherwise
a statement that the name is not registered. `get` MAY probe liveness.

### 10.6 `version`

Prints the implementation's version identifier and exits zero.

### 10.7 Exit codes

Commands MUST use: `0` on success; `2` on a usage error (unknown subcommand,
invalid flag, malformed argument or target); and a non-zero value other than `2`
(RECOMMENDED `1`) on any other runtime failure, including an unreachable control
channel or a rejected authorization (§12.5).

---

## 11. Explicit Relays and the Control Channel (Per-User Mode)

### 11.1 Model

An explicit relay is a deliberate mapping from a service name to a target,
created and destroyed through the CLI and held by the per-user daemon in the
relay registry. Explicit relays exist so that services which cannot, or should
not, use the conventional socket convention can still be reached as
`<name>.localhost`, and take precedence over conventional sockets of the same
name (§6.3). Explicit relays remain within the invoking user's authority: the
daemon runs as the user and dials only what the user could already reach.

### 11.2 Target grammar

```abnf
target        = tcp-target / unix-target
tcp-target    = "tcp://" [ ipv4-loopback ] ":" port   ; host defaults to 127.0.0.1
unix-target   = "unix:" abs-path                       ; abs-path begins with "/"
ipv4-loopback = "127." 1*3DIGIT "." 1*3DIGIT "." 1*3DIGIT   ; within 127.0.0.0/8
port          = 1*5DIGIT                                ; 1–65535
```

The CLI MUST reject a `tcp://` target whose host is not within `127.0.0.0/8`. An
IPv6 literal such as `tcp://[::1]:8080` MUST be refused with a diagnostic (the
IPv6 deferral, §17, made explicit at the CLI boundary). A `tcp://` target with
the host omitted MUST default to `127.0.0.1`. A `unix:` target MUST be an
absolute path. Targets MUST be stored in normalized form.

### 11.3 Names

Explicit relay names MUST satisfy §5.1 and §5.2 and MUST NOT be `control` (§5.3).

### 11.4 Registration semantics

Registering for an unused name creates the relay; registering for a name that
already has one MUST be refused unless overwrite is requested (`--force`). Removal
deletes the entry if present and is an error otherwise. Each name maps to at most
one explicit relay.

### 11.5 Persistence

An explicit relay is persistent by default: the daemon MUST record it in the
relay registry within the data directory such that it is restored on the next
startup. A relay registered with `--ephemeral` MUST NOT be persisted and MUST NOT
be restored after a restart. The on-disk representation is an implementation
detail; the daemon MUST persist changes durably and create the data directory and
any registry file with owner-only permissions.

### 11.6 Control channel

The per-user daemon MUST serve a control channel on a Unix domain stream socket
at `<sockets-dir>/control.sock` (§16). The socket MUST be owner-accessible only
(the enclosing sockets directory is mode `0700`, and the socket file SHOULD be
mode `0600`). The relay subcommands communicate over this socket.

The control channel MUST use HTTP/1.1 over the Unix socket. The request and
response encoding is an internal detail of the CLI-to-daemon hop and is not
observable by clients (§2.2); an implementation MAY use JSON, or a typed RPC
encoding such as Go `gob`, or any other self-consistent encoding, provided the
CLI and daemon agree. This implementation uses `gob`-encoded typed RPC (via the
`httphelp` helpers). Neither the encoding nor the endpoint binding below is
normative.

One workable endpoint binding, given for illustration only:

```
GET    /relays          → list explicit relays
GET    /relays/<name>   → get one explicit relay
PUT    /relays/<name>   → add/overwrite  (body: {"target": "...", "ephemeral": bool, "force": bool})
DELETE /relays/<name>   → remove
```

The daemon MUST apply the validation rules of §5 and §11.2 on the server side,
not solely in the CLI, so the registry cannot be brought into an invalid state by
a direct control-channel client. This server-side validation requirement holds
regardless of the wire encoding chosen.

---

## 12. System Daemon and Delegation

### 12.1 Overview

The system daemon (`run --system`) is a privileged daemon whose sole
function is user delegation: routing `*.<user>.localhost` to that user's per-user
daemon over a Unix socket the user owns. It MUST NOT serve conventional
discovery, explicit relays, or the relay control API (§11); it serves only the
delegation verbs (§12.7). A two-label host `<name>.localhost` is therefore not
routable by the system daemon and MUST return `404`; only delegated three-label
hosts are routable. The system daemon serves plain HTTP by default and serves
HTTPS when supplied a name-constrained CA, terminating TLS centrally and minting
per-user leaf certificates on demand (§13.6).

### 12.2 System paths and listener

In system mode:

- The listener default is `127.0.0.1:80` (override `-addr`), the privileged port
  that motivates running as root. When `-tls-ca-crt` and `-tls-ca-key` are
  supplied the listener serves HTTPS (§13.6); operators typically pair this with
  `-addr 127.0.0.1:443`.
- The sockets directory and data directory are resolved from `-sockets-dir` and
  `-data-dir`. Implementations SHOULD default them to conventional
  system locations; these defaults are RECOMMENDED, not normatively fixed, and
  MAY differ by platform or distribution. This specification does not mandate any
  particular filesystem path.
- The control socket is `<sockets-dir>/control.sock`.

Because self-service `add-user` must be reachable by any user, the system control
socket MUST be world-connectable (the enclosing directory traversable, e.g.
`0755`, and the socket file mode `0666`). This is safe because authorization is
performed in-band via peer credentials (§12.5), not by filesystem permissions.
The system data directory MUST be owner-only (root).

### 12.3 Privilege

The system daemon runs as root (it owns the privileged port regardless). Root
bypasses directory permissions, which is what lets it dial into a user's
otherwise-private runtime directory to reach the delegation target. No privilege
beyond this is required, and none is granted to callers.

### 12.4 Delegation routing

For a normalized host (§6.1) the system daemon MUST apply:

```abnf
delegated-host = service-name "." user-label "." "localhost"
user-label     = service-name          ; the delegated username, a valid DNS label
```

A host with fewer than three labels, or more than three, MUST return `404`;
delegation depth is capped at exactly one user label and the daemon MUST NOT
recurse. Given a valid three-label host, the daemon MUST:

1. Look up a delegation for `<user>`. If none exists, return `404`.
2. Strip the user label, rewriting the forwarded `Host` to `<name>.localhost`
   and preserving the original three-label host in `X-Forwarded-Host` (§7.3).
3. Forward to the delegation's target Unix socket, subject to the ownership check
   in §12.5 and the connect timeout of §6.3.
4. On dial failure or a failed ownership check, return `502`.

Two-label and three-label routing are disjoint by construction: a two-label host
is never matched against a delegation, and a three-label host is only ever
matched against delegations.

### 12.5 Authorization

Delegation rests on two independent checks:

1. **Caller authentication (who may register).** The system daemon MUST read the
   peer's UID from the control socket (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED`
   on macOS) and map it to a username. A non-root caller MAY create, replace, or
   remove a delegation only for their own username. Only root MAY name another
   user. A username that is not a valid DNS label (§5.1) cannot be delegated.
2. **Target ownership (where root may forward).** A delegation target MUST be a
   Unix socket owned by the delegated user. The authoritative check is performed
   at connection time: after connecting to the target, the daemon MUST read the
   listening peer's credentials (`SO_PEERCRED`/`LOCAL_PEERCRED`) and MUST require
   that UID to equal the delegated user's UID; otherwise it MUST NOT proxy and
   MUST return `502`. A cheaper owner-and-symlink precheck SHOULD be performed at
   `add-user` time for a friendly error, but the connect-time credential check is
   the authoritative, TOCTOU-safe gate.

These give a `--user` delegation a single binding: the subtree label, the
delegated username, and the target socket's owning UID are all the same user.

### 12.6 Delegation entries and persistence

A delegation maps one user (the subtree label) to one target Unix socket. Each
user has at most one delegation; `add-user` is an upsert (re-running it replaces
the target, with no `--force` needed). Delegation targets MUST be Unix sockets;
TCP targets are not permitted for delegation in version 1. Delegations are
persistent: the daemon MUST record them in the system data directory and restore
them on startup. A delegation presupposes the user's per-user daemon is running
(operators SHOULD enable lingering); when it is not, routing returns `502`.

### 12.7 Delegation verbs

```
localrelay add-user [<user>] <uds-target>
localrelay remove-user [<user>]
localrelay list-users
```

- `add-user` registers or replaces the caller's delegation to `<uds-target>` (a
  `unix:` target per §11.2). `<user>` defaults to the caller's authenticated
  identity and MAY be given only by root (§12.5).
- `remove-user` removes the caller's delegation; root MAY name another user.
- `list-users` lists the caller's delegation; root sees all.

These verbs address the system control socket (§12.2), which uses the same
transport and encoding as the per-user control channel — HTTP/1.1 over the Unix
socket, with an implementation-chosen body encoding (§11.6; `gob` typed RPC in
this implementation) — exposing delegation rather than relay endpoints. The system daemon MUST reject the relay verbs (`add`, `remove`,
`get`, `list`) and serve only the three delegation verbs.

---

## 13. Transport Security (HTTPS)

### 13.1 Overview

HTTPS is OPTIONAL and disabled by default. In per-user mode it is enabled by
supplying a certificate and key; the daemon then terminates TLS at its listener
and continues to reach backends over plaintext HTTP on the internal hop (Unix
socket or loopback TCP). Enabling HTTPS changes only the client-facing
transport; routing (§6), forwarding (§7), relays (§11), and delegation (§12) are
otherwise unchanged. The system daemon terminates HTTPS centrally under a
supplied name-constrained CA (§13.6).

### 13.2 Per-user configuration

The per-user daemon enables HTTPS when both `-tls-crt` and `-tls-key` are
supplied; supplying only one MUST be a startup error. When enabled, the listener
on `-addr` speaks HTTPS instead of HTTP (§9.2). The certificate SHOULD cover
`*.localhost`, so that every two-label host the per-user daemon routes is
presentable at the TLS layer, and MAY additionally cover `localhost`. The
operator obtains and installs the certificate and its issuing CA out of band
(for example with `mkcert`); the daemon neither generates certificates nor
installs trust anchors in version 1.

### 13.3 Startup validation

At startup with HTTPS enabled the daemon MUST fail if the certificate or key
cannot be loaded, or if they do not form a valid key pair. The daemon SHOULD
warn, but MUST NOT fail, if the certificate does not cover `*.localhost` or does
not chain to a system trust anchor, because trust for a local development CA may
legitimately live only in a browser-specific store (for example Firefox's own
NSS store) rather than the system store. The daemon MUST NOT require its
certificate to be present in any particular trust store.

### 13.4 Effect on forwarding

When a client reaches the daemon over TLS, the daemon MUST set
`X-Forwarded-Proto: https` on the forwarded request (§7.3), so the backend
observes the true client-facing scheme even though the internal hop is plaintext.
All other forwarding semantics are unchanged.

### 13.5 Certificate authority guidance (non-normative)

Because a locally trusted CA installed into a user's trust store can otherwise
vouch for any public name, it is RECOMMENDED that the CA be name-constrained
(RFC 5280 §4.2.1.10) with a permitted dNSName of `localhost` in the
no-leading-dot form. That form matches `localhost` and every subdomain at
arbitrary depth — including two-label `<name>.localhost` and three-label
`<name>.<user>.localhost` — so it bounds the CA's authority to the local
namespace without the single-label limitation of wildcards. A leading-dot form
(`.localhost`) SHOULD NOT be used, since it excludes the bare `localhost` name.
Marking the constraint critical causes conforming verifiers to reject a
mis-issued leaf rather than silently accept it. This guidance is non-normative
and does not affect conformance.

A compliant name-constrained CA of this shape MAY be produced with the
`opensslcmd` package's `CreateRSALocalhostCA` / `CreateECLocalhostCA`, which
emit a self-signed root carrying a critical `nameConstraints` extension
permitting `dNSName:localhost` (no leading dot) together with the loopback IP
ranges `127.0.0.0/8` and `::1`. An implementation MAY expose such generation as
an operator convenience, but this does not relax §13.6: whatever CA is supplied
at `-tls-ca-crt`/`-tls-ca-key`, the system daemon MUST still verify it carries
the required constraint before running.

### 13.6 System-daemon HTTPS

The system daemon serves HTTPS when supplied a CA via `-tls-ca-crt` and
`-tls-ca-key` on `run --system`. It terminates TLS centrally: it completes the
handshake, routes the decrypted request by delegation (§12.4), and proxies
plaintext inward. Central termination is chosen over stream passthrough because
it keeps the plaintext request available for routing and mediation, which future
features (deeper subdomains, cross-user policy) may require; a passthrough design
could route only on the TLS `ClientHello` SNI and would not extend to them.

**CA requirements.** Supplying only one of `-tls-ca-crt` and `-tls-ca-key` MUST
be a startup error. At startup with both flags the daemon MUST fail if the CA
certificate and key cannot be loaded or do not form a valid CA keypair, and MUST
fail if the CA certificate does not carry a name constraint permitting the
dNSName `localhost` in the no-leading-dot form (§13.5) — a CA lacking that
constraint is the unbounded CA the constraint exists to prevent, so the daemon
MUST NOT run with it. The CA key file MUST be root-owned and MUST NOT be group-
or world-readable; the daemon MUST refuse to start otherwise.

**On-demand leaf minting.** For a request whose host is `<name>.<user>.localhost`
where `<user>` has a registered delegation (§12.6), the daemon presents a leaf
certificate for `*.<user>.localhost`, selecting it by the client's SNI. Leaves
are minted on demand under the supplied CA, cached in the system data directory
with the leaf and its key at mode `0600`, root-owned, and re-minted by the daemon
before expiry. The daemon MUST mint only for users that currently have a
delegation; a three-label host for a non-delegated user returns `404` (§12.4)
before any minting, so inbound SNI cannot drive unbounded issuance. There is no
facility to supply a per-user leaf externally in version 1: the daemon owns the
full mint-and-renew lifecycle for every leaf, which is why external leaves —
whose renewal is unspecified — are excluded.

**Trust.** Establishing trust for the constrained CA in each delegated user's
trust store is an operator and deployment step, out of scope for the daemon,
which neither installs nor removes trust anchors. Because the CA is constrained to
`localhost`, installing it is bounded in authority (§13.5).

Once the system daemon terminates TLS, the internal hop to the per-user daemon
remains plaintext HTTP, and the system daemon sets `X-Forwarded-Proto: https` and
the rewritten headers of §7.3 and §12.4.

---

## 14. Security Considerations

**Per-user isolation is provided by the sockets directory.** With the default
layout, per-user sockets live under `XDG_RUNTIME_DIR`
(`/run/user/<uid>/localrelay/`, mode `0700`), so only that user (and root) can
create, dial, or list them or reach the per-user control socket. Name squatting
by other users is structurally impossible.

**Explicit relays add no privilege.** The per-user daemon dials only loopback
TCP ports and Unix paths the invoking user could already reach.

**The system control socket is intentionally world-connectable**, unlike the
per-user one. Authorization is done in-band by peer credentials (§12.5), never by
filesystem permissions. The two checks are distinct: caller authentication
governs who may register a delegation (self, or root for anyone), and target
ownership governs where root will forward (only into a socket owned by the
delegated user). Together they prevent both registry squatting and the
confused-deputy escalation in which root could be induced to connect somewhere
the caller could not.

**Loopback only.** Listeners MUST bind a loopback address, and explicit TCP
targets MUST be loopback (§11.2).

**Path confinement.** Because service names are single labels, the daemon never
derives a conventional socket path outside its sockets directory. Explicit
`unix:` targets and delegation targets are, by design, paths supplied by the user
and (for delegation) verified by owner credentials at connect time.

**Origin and cookie scoping.** All services share the `.localhost` suffix and are
same-site under browser rules for `localhost`. The daemon MUST NOT set cookies of
its own or broaden cookie scope, and implementers MUST NOT introduce
cross-service cookie behavior.

---

## 15. Platform Requirements

**Name resolution.** `<name>.localhost` is resolved to a loopback address by the
*client*, before any connection reaches the daemon. The daemon accepts only
already-connected TCP and performs no name resolution for inbound clients;
consequently it cannot make `*.localhost` resolve on a client's behalf.

External DNS is not required. RFC 6761 reserves `localhost` and every name
beneath it for the loopback address, and the primary clients — current versions
of Chrome, Edge, and Firefox — implement this directly, mapping every
`*.localhost` name (including multi-label delegated hosts) to `127.0.0.1` and
`::1` without consulting the system resolver and without any hosts-file or DNS
configuration. For these clients the no-configuration property holds on every
platform.

The exceptions are narrow: a non-browser client (for example `curl` or anything
using `getaddrinfo`) falls back to the system resolver, which may not synthesize
`*.localhost`, and some browsers on some platforms have only recently adopted the
behavior (see the macOS note below). Because resolution is client-side, the
daemon cannot override such a client. An OPTIONAL local DNS responder is noted as
deferred (§17); it would require a one-time OS resolver hook and a privileged
listener, so it relocates rather than eliminates platform-specific setup.

**Session lifetime.** `XDG_RUNTIME_DIR` (`/run/user/<uid>`) is session-scoped and
removed on last logout. Operators who require per-user services (or delegation
targets) to persist across logout must enable lingering for the user (e.g.
`loginctl enable-linger`). This is a deployment requirement, not daemon behavior.

**Socket path limits.** Implementations MUST honor the `sun_path` limit of the
target platform (108 bytes Linux, 104 bytes macOS) per §5.2.

**macOS.** macOS is a secondary target. Chrome and Firefox map `*.localhost` to
loopback on macOS as on Linux, so browser clients need no configuration. Safari
adopted this behavior only recently, and the macOS system resolver does not
synthesize `*.localhost` by default, so Safari on older macOS releases and
non-browser clients may require additional resolver configuration. Version 1
implementations SHOULD document the current state when macOS support is
validated.

---

## 16. Reserved Names and Forward Compatibility

- The service name `control` is reserved. It MUST NOT be routed to a backend
  (§5.3), MUST NOT be registrable as an explicit relay (§11.3), and
  `control.sock` MUST be excluded from `list` output (§10.2).
- The path `<sockets-dir>/control.sock` is the control channel (§11.6, §12.2). It
  is not a backend and MUST NOT be treated as a conventional service socket.

The per-user and system control channels share the same wire protocol (HTTP/1.1
over a Unix socket, with an implementation-chosen body encoding — §11.6),
differing only in the verbs each serves. Keeping the `control` name and socket
path reserved ensures the two channels remain distinguishable and that future
management surfaces do not collide with a service name.

---

## 17. Deferred Features (Non-normative)

- **IPv6.** Loopback IPv6 targets (`tcp://[::1]:port`) and IPv6 listeners;
  version 1 rejects IPv6 targets explicitly (§11.2).
- **Arbitrary subtree delegation.** Mapping an arbitrary subtree label to an
  arbitrary (non-user-owned) target — the general primitive deliberately excluded
  so that every version-1 delegation is a user-owned Unix socket (§12). Its
  reintroduction would be a privileged, root-only operation.
- **Optional local DNS responder.** A component answering `*.localhost` with the
  loopback address for non-browser clients or resolvers that do not honor RFC
  6761 (§15).

---

## 18. Conformance

An implementation conforms to this specification if and only if it implements
every requirement expressed with MUST, MUST NOT, REQUIRED, SHALL, or SHALL NOT in
sections 4 through 16. Behavior expressed with SHOULD or MAY is not required for
conformance but, where implemented, MUST be consistent with the semantics
described here.

---

## Appendix A. Collected Grammar (ABNF)

```abnf
service-name   = alnum *( alnum / "-" ) alnum
               / alnum
alnum          = %x61-7A / %x30-39        ; a-z 0-9

routed-host    = service-name "." "localhost"                 ; per-user mode
delegated-host = service-name "." user-label "." "localhost"  ; system mode
user-label     = service-name

socket-path    = sockets-dir "/" service-name ".sock"

target         = tcp-target / unix-target
tcp-target     = "tcp://" [ ipv4-loopback ] ":" port   ; host defaults to 127.0.0.1
unix-target    = "unix:" abs-path                       ; abs-path begins with "/"
ipv4-loopback  = "127." 1*3DIGIT "." 1*3DIGIT "." 1*3DIGIT   ; within 127.0.0.0/8
port           = 1*5DIGIT                                ; 1–65535
```

## Appendix B. Default Constants

| Constant                        | Default value                         | Override        |
|---------------------------------|---------------------------------------|-----------------|
| Per-user listener address       | `127.0.0.1:7900`                      | `-addr`         |
| Per-user sockets directory      | `$XDG_RUNTIME_DIR/localrelay`         | `-sockets-dir`  |
| Per-user sockets dir / socket mode | `0700` / `0600`                    | —               |
| Per-user data directory         | `$XDG_STATE_HOME/localrelay`          | `-data-dir`     |
| Per-user TLS certificate        | none (HTTP unless set)                | `-tls-crt`      |
| Per-user TLS key                | none (HTTP unless set)                | `-tls-key`      |
| System TLS CA certificate       | none (HTTP unless set)                | `-tls-ca-crt`   |
| System TLS CA key               | none (HTTP unless set)                | `-tls-ca-key`   |
| System listener address         | `127.0.0.1:80`                        | `-addr`         |
| System sockets directory        | implementation default (RECOMMENDED, not mandated) | `-sockets-dir` |
| System data directory           | implementation default (RECOMMENDED, not mandated) | `-data-dir` |
| System control socket mode      | `0666` (dir traversable, e.g. `0755`) | —               |
| Data directory mode             | `0700`                                | —               |
| Control socket                  | `<sockets-dir>/control.sock`          | —               |
| Backend connect timeout         | 5 seconds                             | — (fixed in v1) |
| Reserved service name           | `control`                             | —               |
| Conventional socket pattern     | `<name>.sock`                         | —               |

System-mode filesystem locations are deliberately left to the implementation and
deployment; this specification recommends conventional system directories but
mandates none.

*End of specification.*
