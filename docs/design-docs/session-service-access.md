# Host Access to Session Services

Status: Proposed
Scope: TCP access from the developer's Linux host to services in the project session, including published nested
Docker ports.

## Purpose and Intent

### Problem

A developer can open an application or database bound to `127.0.0.1` when running it directly on the host. In an
`agents-safe` session, that loopback address belongs to the session container. The host can reach the session's
Docker bridge address, but a loopback-only service does not accept a connection to that address.

### Worked Example

Before:

```text
Session: app listens on 127.0.0.1:8000
Host:    connect to <session-IP>:8000 -> connection refused
```

After:

```text
Session: app listens on 127.0.0.1:8000
Host:    connect to <session-IP>:8000 -> app accepts, sees client 127.0.0.1
```

The same host-side connection works when an agent starts a nested container later and publishes its port on the
session's `127.0.0.1`. The nested application's view of the client still follows Docker's own port-publishing path.
A nested container that publishes no port remains outside this contract.

### Chosen Shape

The session's network namespace translates incoming IPv4 TCP connections from the Docker host to the same port on
`127.0.0.1`. Destination translation makes loopback-only listeners reachable. Source translation makes the service
see `127.0.0.1`, preserving local-client checks such as PostgreSQL host rules. No process listens on application
ports, so an application can bind `0.0.0.0:8000` after the session starts.

The launcher enables `route_localnet` for the session's ingress interface at container creation. The privileged
session supervisor installs the two translation rules after its nested Docker daemon is ready and before the session
accepts commands. Rules match the session's bridge address and traffic from the host's bridge gateway. Docker's own
rules for published nested ports take precedence; the session rules handle traffic Docker did not translate.

This is a local development convenience. The host, not a remote LAN client, is the intended caller. The source-IP
match is a routing filter, not authentication against a process able to spoof packets on the bridge.

## Contract

### 1. Automatic Access

- Every IPv4 TCP port bound to session loopback is reachable from the host at `<session-IP>:<same-port>` while the
  session is running. The service sees a `127.0.0.1` peer.
- A service bound to `0.0.0.0` can start later and bind its chosen port. It remains reachable from the host at the
  session IP and sees a `127.0.0.1` peer through this path.
- No per-port registration, port reservation, host port publication, or session restart is required.
- The session's IP can change when a new session container is created. The launcher does not promise a stable IP.

### 2. Nested Docker

- A nested container may start at any time. Its port is reachable through the session IP when it publishes that port
  into the session namespace with Docker `-p` or Compose `ports:`.
- Publication on the session's `127.0.0.1` uses the loopback translation. Publication on all session interfaces
  retains Docker's normal routing. The session does not discover or publish ports of nested containers.
- The outer translation makes Docker's session-side published endpoint see a loopback peer. The application inside
  the nested container sees whichever source address nested Docker normally supplies for a published port.
- A nested container without a published port remains inaccessible through the session IP.

### 3. Startup and Lifetime

- `route_localnet` is enabled only in the session container's network namespace, not on the host.
- Failure to configure or verify the network translation fails session startup with an actionable diagnostic. The
  launcher does not silently run a session whose loopback access differs from this contract.
- Network rules are recreated on each container start and are not duplicated when commands reuse a running session.
- Existing host MCP loopback listeners become reachable from the host through the session IP. This is intentional.
  Other containers on the default bridge are not intended callers of the translation rule.

## Boundaries and Non-Goals

- This contract covers IPv4 TCP. UDP and IPv6 loopback access are separate work.
- The host uses the session IP. This feature does not publish ports on host `127.0.0.1` or a LAN interface.
- The feature does not bypass application authentication. It preserves the local peer address seen by the service.
- A service bound only to the session's non-loopback IP is not covered by loopback translation.
- A nested container must publish its own port; automatic discovery of its private IP and unpublished ports is out
  of scope.

## Test Plan

- Unit-test the Docker create request's session-local `route_localnet` setting and ensure relay and maintenance
  containers do not receive it.
- Unit-test the session rule selection, installation order, failure diagnostics, and idempotent restart behavior.
- On a compatible Linux host with Sysbox, prove a loopback-bound TCP service is unreachable before translation and
  reachable through the session IP after startup, with the service observing a `127.0.0.1` peer.
- Start a wildcard-bound service after session readiness and prove it can bind and answer through the session IP.
- Start nested containers after readiness with both loopback and all-interface published ports, and prove the host
  can connect to both. Prove an unpublished nested port remains outside the session-IP path.
- Check that a sibling session does not receive the host-only translation for another session's loopback service.
- Inspect the session and host network configuration to prove no host namespace, host sysctl, or host port mapping
  was added.

## Where the Code Lives

- `internal/launcher/docker_requests.go` and `internal/launcher/dockercli/`: session create request and Docker argv.
- `internal/container/`: supervisor startup, network rule installation, and diagnostics.
- `tests/smoke/`: real Sysbox and nested-Docker boundary checks.
