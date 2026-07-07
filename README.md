# tbox

Docker-compose stack brokering the browser session flow:

```
user -> APISIX -> Keycloak -> APISIX -> service-a (and service-b)
```

APISIX terminates the OIDC dance at `:9080`, then forwards authenticated requests to `service-a:3000` (and `service-b:3001`) with `X-Userinfo` injected. Tracer stack (OpenTelemetry Collector + Tempo + Grafana) is optional and runs on the same network.

## Prerequisites

- Docker Engine + Docker Compose v2
- Host ports free: `9080` (APISIX), `8080` (Keycloak), `3000` (Grafana, if tracer enabled)

## Quick start

```sh
cp .env.example .env
docker compose up -d --build
curl -i http://localhost:9080/
```

`curl` should return a `302` redirect to Keycloak. The Keycloak login UI is at http://localhost:8080.

## Login flow

1. Open http://localhost:9080/ in a browser.
2. Log in with `tbox` / `password`.
3. Expect HTML: `Hello, tbox! (role: tbox-user)`.

## Verify other routes & tracer

- **service-b** — same login flow, different role:
  ```sh
  curl -i http://localhost:9080/service-b/
  ```
  Expect `Hello, tbox! (role: tbox-user-service-b)`.
- **tracer stack** — bring up separately, then open Grafana at http://localhost:3000:
  ```sh
  docker compose -f tracer/docker-compose.tracer.yml up -d
  ```
  Login with `GRAFANA_ADMIN_*` from `.env`. Tempo is the auto-provisioned datasource (Explore -> query `{ resource.service.name = "apisix" }` or `= "service-a"` / `= "service-b"`).

## Teardown

```sh
docker compose down -v
```

`-v` removes the Keycloak DB volume. Repeat with `-f tracer/docker-compose.tracer.yml` to drop the tracer stack too.

## Notes

- The demo client secret is committed verbatim to `keycloak/tbox-realm.json` for one-command setup. **Rotate it before any non-demo use** — see `SECRET_REGEN.md`.
- For architecture, authz rules, tracer pipeline details, and the custom agent system, see `AGENTS.md` in this repo.