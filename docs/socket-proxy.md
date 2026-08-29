# Docker Socket Proxy Requirements

`vibe-backup` never gets the raw Docker socket. The appliance (BK-10a)
wires it through a filtering proxy (e.g. tecnativa/docker-socket-proxy
or linuxserver/socket-proxy); standalone deployments must do the same.
Sentinel flags a bare socket mount, and it is right to.

## Endpoints this sidecar actually calls

| Endpoint | Method | Why |
|---|---|---|
| `/containers/json` | GET | discover labeled module containers |
| `/containers/{id}/json` | GET | image tags, env (secrets), mounts, networks |
| `/containers/{id}/archive` | GET | read contracts; capture volumes |
| `/containers/{id}/archive` | PUT | ship dumps into DB containers for restore/drill |
| `/containers/{id}/exec` + `/exec/*` | POST/GET | pg_dump/pg_restore, module hooks, canaries |
| `/containers/create`, `/containers/{id}/start`, `/stop`, `DELETE` | POST/DELETE | the ephemeral drill database only |
| `/images/create` | POST | pull the recorded DB image for drills |

With tecnativa's proxy that means: `CONTAINERS=1`, `EXEC=1`,
`IMAGES=1`, `POST=1`, `ALLOW_START=1`, `ALLOW_STOP=1`, and archive
access. Everything else stays denied (networks, swarm, system, build,
raw events).

## Honest threat assessment (plan §4.2 deviation, recorded)

The build plan's shorthand — "restricted to CONTAINERS:1 (list/inspect
only)" — is not achievable for the design the same plan specifies:
exec-based dumps, module hooks, decrypt canaries, and the drill
container all require exec and lifecycle rights. Exec into containers
is root-equivalent **on those containers**; the proxy still buys real
reduction (no host-level socket verbs, no network/volume/image tampering
beyond pull, no access to unlabeled containers' create/delete), and the
recovery path never depends on the appliance's honesty: immutability is
enforced by B2, not by this process.

If a future Docker release ships fine-grained authz for exec targets,
tighten this file first and the code second.
