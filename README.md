# localrelay

A small reverse-proxy daemon that routes HTTP and HTTPS requests for
`*.localhost` to other local services, with a built-in certificate authority
that is name-constrained `localhost`.

Root can configure forwarding on ports 80, 443 as:

    http://filer.localhost/index.html => http://127.0.0.1:8080/index.html

    https://tradebot.username.localhost/index.html => /home/username/tradebot.sock

and non-root users can also configure similar setup, but on unprivileged ports.

**HTTPS** is enabled by pointing `-ca-dir` at a local CA; the daemon then mints
a certificate for every `*.localhost` host on the fly, so all local names are
served over TLS transparently.

Applications can also register themselves by creating `<app>.sock` unix domain
sockets in a `-sockets-dir` special directory.

The reserved name `control` is used for the daemon's control socket and is
never routable.

## Install

Installation needs a version 1.24 or later Go compiler.

```sh
go install github.com/visvasity/localrelay@latest
```

This puts the `localrelay` binary in `~/go/bin` directory.

## Usage

The following commands demonstrate how to setup and use this service:

### As a non-root user (HTTP, without Certificate Authority)

```sh
localrelay run -background -http-port 1080

# Add TCP and UNIX domain socket service backends.
localrelay add filer http://127.0.0.1:8080
localrelay add tradebot http:///home/username/tradebot.sock

# Access them over HTTP with a hostname and port number.
curl http://filer.localhost:1080/...
curl http://tradebot.localhost:1080/...
```

### As a root (HTTPS with a Certificate Authority)

```sh
# Setup a name constrained Root CA for *.localhost.
sudo localrelay ca init /var/lib/localrelay/ca
sudo localrelay ca install -system /var/lib/localrelay/ca

# Start the service with HTTPS endpoint.
sudo localrelay run -background --ca-dir /var/lib/localrelay/ca

# Enable username forwarding.
sudo localrelay add -user username /var/run/user/$(id -u username)/localrelay.sock

# Access them at *.username.localhost subdomain.
curl http://filer.username.localhost/index.html
curl https://filer.username.localhost/index.html
```

Note that non-root users can also setup HTTPS relays, but they may not be able
add the trusted root certificate authority for *.localhost.

## Config defaults

The following defaults are used for the ports and directory paths based on the
user id.

| | root (uid 0) | non-root |
|---|---|---|
| HTTP port | `80` | `1080` |
| HTTPS port | `443` | `1443` |
| Sockets dir (runtime) | `/run/localrelay` | `$HOME/.sockets` |
| Data dir (persistent) | `/var/lib/localrelay` | `$HOME/.localrelay` |

Any of these can be overridden with `-http-port`, `-https-port`, `-sockets-dir`,
`-data-dir`.

## Delegate to per-user relays

Root can delegate to per-user relays at `*.<user>.localhost` subdomains
directed through Unix domain socket **owned by that user**. This lets an
unprivileged user own everything under their subdomain while the root daemon
terminates TLS centrally.

```sh
# 'alice' runs her own service on a Unix socket she owns, e.g. /run/alice/app.sock

# Register the delegation (root):
sudo localrelay add -user alice /run/alice/app.sock

# Every <name>.alice.localhost now forwards to alice's socket. The daemon strips
# the user label, so her backend sees Host: <name>.localhost.
curl https://web.alice.localhost/          # -> alice's backend sees web.localhost
curl https://api.alice.localhost/          # -> same socket, api.localhost
```

Ownership of the Unix domain socket is verified on **every** request: if the
socket is missing or is not owned by `alice`, that request gets a `404`.

## Trusting the CA

The daemon mints its TLS certificates from the CA in `-ca-dir`, so clients must
trust that CA. `localrelay ca install <ca-dir>` adds it to a trust store, and
`localrelay ca uninstall <ca-dir>` removes it. The same flags select the same
targets for both commands:

| Flag | Target | Root? |
|---|---|---|
| _(none)_ | The current user's browsers — whichever of Firefox and Chrome are present | no |
| `-firefox` | The current user's Firefox profiles | no |
| `-chrome` | The current user's Chrome/Chromium NSS store | no |
| `-system` | The host-wide system store (used by `curl`, `git`, language runtimes, …) | yes |

```sh
# Trust the CA in your own browsers (no root):
localrelay ca install $HOME/local-ca

# Trust it host-wide (needs root):
sudo localrelay ca install -system $HOME/local-ca

# Undo either, the same way:
localrelay ca uninstall $HOME/local-ca
sudo localrelay ca uninstall -system $HOME/local-ca
```

Browser trust uses the per-user NSS databases Firefox and Chrome keep. It needs
the `certutil` tool — `libnss3-tools` on Debian/Ubuntu, `nss-tools` on
Fedora/RHEL, `nss` on Arch and macOS (Homebrew) — but no root; restart the
browser afterwards. A browser that isn't installed is skipped.

The `-system` store is located per platform: `update-ca-certificates`
(Debian/Ubuntu, openSUSE), `update-ca-trust` (Fedora/RHEL), or `trust`
(Arch) on Linux, and the System keychain via `security` on macOS.

`ca uninstall` never removes an unrelated certificate: it deletes an entry only
after confirming the stored certificate is a CA name-constrained to `localhost`.

Because the CA certificate is public — only `key.pem` is secret, kept at mode
`0600` — a root-created CA directory stays world-readable, so any user can
`ca install` from the same `<ca-dir>`.

## Command reference

| Command | Purpose |
|---|---|
| `localrelay run` | Run the daemon. `-ca-dir <dir>` enables HTTPS (mints per-host certs on the fly). |
| `localrelay add <name> <target>` | Register a relay. `-clean-url` rewrites the forwarded Host to the backend host. |
| `localrelay add -user <user> <uds-socket>` | Delegate `*.<user>.localhost` to a user-owned Unix socket. |
| `localrelay list` | List all relays, delegations, and conventional sockets. |
| `localrelay ca init <dir>` | Create a name-constrained localhost root CA (`key.pem`, `cert.pem`). |
| `localrelay ca install <ca-dir>` | Trust the CA (browsers by default; `-firefox`/`-chrome`/`-system`). See [Trusting the CA](#trusting-the-ca). |
| `localrelay ca uninstall <ca-dir>` | Untrust the CA (same flags as `install`). |

## Notes

- **Name resolution.** `*.localhost` resolves to the loopback address on
  systems using `systemd-resolved` (per RFC 6761). Elsewhere, add the names to
  `/etc/hosts` or test with `curl --resolve filer.localhost:1443:127.0.0.1
  …`. Modern browsers resolve `*.localhost` to `127.0.0.1` automatically.
- **On-the-fly certificates.** With `-ca-dir`, the daemon mints (and caches) a
  distinct leaf per host, chosen by the TLS SNI. The CA must be name-constrained
  to `localhost` (as produced by `ca init`); the daemon refuses to start
  otherwise, and refuses the handshake for any non-`localhost` SNI. A client that
  sends no SNI is served the `localhost` certificate.
- **`ca cert` is optional.** The daemon mints its own certs from `-ca-dir`, so
  `ca cert` is only needed when you want a cert *file* for something else (a
  different server, testing). A bare `*.localhost` wildcard is not honored by TLS
  clients (RFC 6125 forbids a wildcard directly left of a TLD); use per-name
  leaves or `*.<label>.localhost`.
