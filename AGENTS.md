# AGENTS.md

## Stack at a glance

`tbox/` is a docker-compose stack that brokers **user → APISIX → Keycloak → APISIX → service-a** for browser sessions.

```
                 :9080 (host)
user ─► APISIX ─┐
                 │ (no token / no session)
                 ▼
            Keycloak :8080
            (login UI, realm=tbox,
             client=apisix-gateway,
             role=tbox-user)
                 │
                 │ (callback /cb)
                 ▼
              APISIX
                 │  injects X-Userinfo
                 ▼
          service-a :3000 (internal only)
          role check on realm_access.roles
```

| Service     | Host port | Internal  | Image                                  |
| ----------- | --------- | --------- | -------------------------------------- |
| apisix      | 9080      | 9080      | apache/apisix:3.13.0-debian            |
| keycloak    | 8080      | 8080      | quay.io/keycloak/keycloak:24.0         |
| keycloak-db | -         | 5432      | postgres:16-alpine                     |
| etcd        | -         | 2379/2380 | quay.io/coreos/etcd:v3.5.13            |
| service-a   | -         | 3000      | built from `services/service-a`        |
| service-b   | -         | 3001      | built from `services/service-b`        |
| otel-collector | -      | 4317/4318 | otel/opentelemetry-collector-contrib:0.155.0 |
| tempo       | -         | 3200/4317 | grafana/tempo:2.10.7                   |
| grafana     | 3000      | 3000      | grafana/grafana:13.0.3                 |

`service-a` has **no host port** on purpose. It is only reachable via APISIX, which adds `X-Userinfo` after validating the OIDC token.

## Layout

```
tbox/
├── docker-compose.yml          6 services + shared bridge network `tbox-net`
│                               (includes tracer/docker-compose.tracer.yml)
├── .env / .env.example         secret-bearing env; .env is gitignored
├── .gitignore                  excludes .env
├── SECRET_REGEN.md             how to rotate the demo client secret
├── keycloak/
│   └── tbox-realm.json         realm export: realm `tbox`, client `apisix-gateway`,
│                               role `tbox-user`, user `tbox/password`
├── apisix/
│   ├── apisix.yaml             standalone routes/upstreams (svc-a-root + svc-b-root) + openid-connect plugin
│                               + opentelemetry plugin + plugin_metadata
│   └── conf/config.yaml        admin disabled, etcd host = etcd:2379
│                               + plugins: allowlist (incl. opentelemetry)
├── tracer/                     OpenTelemetry tracer stack (included into main compose)
│   ├── docker-compose.tracer.yml     otel-collector + tempo + tempo-init + grafana
│   ├── otel-collector/config.yaml    pipeline: otlp -> memory_limiter -> batch -> tempo
│   ├── tempo/tempo.yaml              local-fs storage, OTLP on :4317, HTTP API :3200
│   └── grafana/provisioning/         auto-provisioned Tempo datasource + dashboards provider
└── services/
    ├── service-a/              in compose; authz middleware in server.go
    │   ├── server.go           Fiber v3 on :3000, role check on X-Userinfo
    │   │                       + initTracer (OTLP/HTTP) + fiberotel.Middleware
    │   ├── go.mod / go.sum     pinned to go 1.26.4
    │   ├── Dockerfile          golang:1.26.4-alpine → distroless
    │   └── .dockerignore
    └── service-b/              in compose; authz middleware same pattern as
                                service-a (extractIdentity + handler); port :3001.
                                Role check is enforced in APISIX's
                                serverless-pre-function Lua plugin
                                (not in Go).
        ├── server.go           Fiber v3 on :3001
        │                       + initTracer (OTLP/HTTP) + fiberotel.Middleware
        ├── go.mod / go.sum     pinned to go 1.26.4
        └── Dockerfile          golang:1.26.4-alpine → distroless, EXPOSE 3001
```

## Bring-up

```sh
cp .env.example .env                # demo values already work
docker compose up -d --build        # first up also builds service-a/service-b images
curl -i http://localhost:9080/      # expect 302 -> Keycloak
# browser: login as tbox / password  -> 200 "Hello, tbox! (role: tbox-user)"
curl -i http://localhost:9080/service-b/  # expect 302 -> Keycloak
# browser: login as tbox / password  -> 200 "Hello, tbox! (role: tbox-user-service-b)"
docker compose down -v              # tear down + delete Keycloak DB volume
```

- **APISIX discovery URL** ใน `apisix/apisix.yaml` ใช้ host gateway IP (`172.26.0.1`) แทน internal hostname `keycloak` เพื่อให้ browser resolve ได้เมื่อ follow redirect compose pin IPAM subnet ไว้แล้ว (`172.26.0.0/16`, gateway `172.26.0.1`) ดังนั้น `docker compose down && up` แล้ว IP ไม่เปลี่ยน แต่คง `docker network inspect tbox_tbox-net --format '{{range .IPAM.Config}}{{.Gateway}}{{end}}'` ไว้เผื่อตรวจสอบ

Bypass checks (manual):

```sh
# Hit service-a directly (bypassing APISIX) -> 401 "missing identity header"
docker compose exec service-a /app/server   # unreachable from host without exec
# Or another container on the same network:
docker run --rm --network tbox_tbox-net alpine wget -qO- service-a:3000/
# -> 401
# Same for service-b:
docker run --rm --network tbox_tbox-net alpine wget -qO- service-b:3001/
# -> 401
```

## Authz rules

- APISIX `openid-connect` plugin (see `apisix/apisix.yaml`) handles the redirect dance and token exchange. After successful auth, it forwards the request to `service-a:3000` and adds headers:
  - `X-Userinfo` (JSON of userinfo claims; default behavior of `set_userinfo_header: true`)
  - `X-ID-Token` (raw ID token)
  - `X-Access-Token` (raw access token)
- `services/service-a/server.go` `extractIdentity` (top of file) base64-decodes `X-Userinfo` then JSON-decodes it; `requireRole` (below) enforces the role:
  - 401 if `X-Userinfo` missing or unparseable
  - 403 if `realm_access.roles` does not include the required role
  - else handler responds with HTML: `<h1>Hello, <preferred_username>! (role: tbox-user)</h1>` plus `X-Userinfo`, `X-ID-Token`, `X-Access-Token` blocks

`services/service-b/server.go` follows the same authz pattern, with role check enforced in APISIX's `serverless-pre-function` Lua plugin (required role `tbox-user-service-b`), listening on `:3001`.

To change the required role: edit the `requiredRole` constant in the relevant service's `server.go` AND add the role to `keycloak/tbox-realm.json` (`roles.realm[]`) AND assign it to `users[].realmRoles`.

## Tracer stack

- 3 new services: `otel-collector`, `tempo`, `grafana` — defined in `tracer/docker-compose.tracer.yml` and merged into the main compose via `include:` at the top of `docker-compose.yml`.
  - `otel-collector` (`otel/opentelemetry-collector-contrib:0.155.0`) — OTLP receivers on `:4317` (gRPC) + `:4318` (HTTP); exports to Tempo via OTLP/gRPC.
  - `tempo` (`grafana/tempo:2.10.7` — **not** 3.x, breaking change) — local-filesystem trace storage, OTLP ingest on `:4317`, HTTP API on `:3200` for Grafana.
  - `grafana` (`grafana/grafana:13.0.3`) — UI + Tempo datasource pre-provisioned at `tracer/grafana/provisioning/`.
- All three join the existing bridge `tbox-net` (external network reference `tbox_tbox-net` in the included file).
- Only Grafana has a host port (`:3000`). Collector and Tempo are internal only.
- Tempo runs as uid `10001`; a one-shot `tempo-init` busybox container chowns the `tempo-data` volume before Tempo starts.
- APISIX publishes traces via its `opentelemetry` plugin (only OTLP/HTTP is supported; plugin metadata in `apisix/apisix.yaml` points at `otel-collector:4318`). APISIX injects `traceparent` into outbound requests; the Go services' `fiberotel` middleware extracts it.
- Go services use `github.com/gofiber/contrib/v3/otel v1.2.1` (aliased `fiberotel` in `server.go` to avoid collision with `go.opentelemetry.io/otel`). The `otel.Middleware` is the FIRST `app.Use(...)` call so the server span is the outermost; `initTracer` runs before `app.Use(...)` and registers W3C `TraceContext` + `Baggage` propagators.
- Each Go service sets `service.name` via the OTel resource (in `initTracer`) and also emits `X-Trace-Id` response header via `fiberotel.WithTraceResponseHeader("X-Trace-Id")` for browser-side correlation.
- Bring-up order: main stack first (`docker compose up -d`), then the tracer (`docker compose -f tracer/docker-compose.tracer.yml up -d`).
- Verify URLs:
  - Grafana UI: http://localhost:3000 (login with `GRAFANA_ADMIN_*` from `.env`)
  - Tempo readiness: `docker compose -f tracer/docker-compose.tracer.yml exec tempo wget -qO- http://127.0.0.1:3200/ready`
- Where to find traces: Grafana → Explore → Tempo (default datasource) → query `{ resource.service.name = "apisix" }` or `= "service-a"` or `= "service-b"`.
- The included file's `external: true` network name `tbox_tbox-net` must stay in sync with the main compose's network. If you ever rename the network in `docker-compose.yml`, update `tracer/docker-compose.tracer.yml:108-110` to match.

## Secrets

| Variable                 | Lives in                                  | Notes |
| ------------------------ | ----------------------------------------- | ----- |
| `KEYCLOAK_CLIENT_SECRET` | `.env` + `keycloak/tbox-realm.json` client | Demo value committed for one-command bring-up. **Rotate before any non-demo use** — see `SECRET_REGEN.md`. |
| `KEYCLOAK_ADMIN_PASSWORD`| `.env`                                    | Admin user at http://localhost:8080/admin (master realm) |
| `POSTGRES_PASSWORD`      | `.env`                                    | Keycloak DB only |
| `APISIX_SESSION_SECRET`  | `.env`                                    | Must be ≥16 chars; used to encrypt session cookie |

Important: the demo client secret lives verbatim in `keycloak/tbox-realm.json` for one-command setup. If you keep that file in version control, treat the secret as public and rotate immediately in any real deployment.

## Custom agent system (`.opencode/agents/`)

This repo ships a custom three-agent hierarchy. **Do not bypass it** and do not run ad-hoc as a default opencode agent — `bob` is the primary and is wired to this flow.

| Agent    | Role                     | Mode     | Key constraint |
| -------- | ------------------------ | -------- | -------------- |
| `bob`    | Coordinator / dispatcher | primary  | All tools denied except `task` (only to `worker`/`library`), `question`, `todowrite` |
| `library`| Read-only researcher     | subagent | `edit`/`write` denied; `bash` is an allowlist (ls, cat, head, tail, find, rg, grep, git read-only) |
| `worker` | Executor                 | subagent | May only call `library` via `task`; no other subagents |

Workflow bob enforces: summarize → clarify (≤3 `question` calls) → plan → preview `task` calls → **wait for user approval** → dispatch. Worker returns "What I did / Verification / Issues".

If the user is using the default opencode agent (no `.opencode/agents/bob.md` resolved), surface this and ask before proceeding.

## Go services

- Each `services/*` is its **own Go module** (separate `go.mod`/`go.sum`). There is no root module — run `go` commands from inside a service dir, not from the repo root.
- Go toolchain pinned to `1.26.4` in both `go.mod` files; ensure that version is available locally or `go.work`-style version managers will fail.
- Both services use `github.com/gofiber/fiber/v3`. `service-a` listens on `:3000`; `service-b` listens on `:3001`. They run together without port collision.
- No `Makefile`, no test files, no lint config, no CI. `go test ./...` and `go vet ./...` work per-service but there is nothing to run yet.
- Both services share the same authz pattern (`extractIdentity` + `requireRole`). `service-a` requires role `tbox-user`; `service-b` requires role `tbox-user-service-b`. Each is a valid standalone starting point and both are wired into compose.

## Conventions

- No top-level `README`, no `LICENSE`, no CI workflows, no pre-commit config. Don't go looking for them.
- The `.opencode/node_modules/`, `package.json`, `package-lock.json` under `.opencode/` are gitignored (see `.opencode/.gitignore`); don't commit them.
- Comments in code: there are none in the original stubs, but `services/service-a/server.go` does use comments sparingly where fiber idioms need explanation. Match that style — don't add gratuitous commentary.
- `.env` is gitignored; demo client secret is intentionally also in `keycloak/tbox-realm.json` so the stack runs from one command. See `SECRET_REGEN.md`.
