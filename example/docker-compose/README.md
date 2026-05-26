# emission + Traefik (Docker Compose)

Drop-in stack that runs emission behind [Traefik](https://traefik.io)
with automatic Let's Encrypt HTTPS.

## Stack

- **traefik** — reverse proxy, TLS termination, ACME-TLS challenge.
  Discovery is via the Docker socket; `exposedByDefault=false` means only
  containers with explicit `traefik.enable=true` are routed.
- **emission** — `serve` mode with auth + UI behind the proxy. Bound
  on the internal Docker network; not exposed on the host.

## Prerequisites

- Docker Engine with the Compose v2 plugin.
- A domain pointing at the host (A/AAAA record).
- Ports **80** and **443** reachable from the public internet — Let's
  Encrypt validates the TLS challenge on 443.

## Use

1. Copy the env template and edit:
   ```sh
   cp .env.example .env
   $EDITOR .env        # set EMISSION_HOST and ACME_EMAIL
   ```
2. Start the stack:
   ```sh
   docker compose up -d
   ```
3. **Within 15 minutes of first boot**, open `https://<EMISSION_HOST>` in a
   browser that supports passkeys. Register the admin device — this is the
   bootstrap window; after it closes (or the container restarts with no
   credentials), the only way back in is `docker compose down -v` (which
   wipes everything) or manually deleting `auth.json` from the `auth`
   volume.
4. Once admin is registered, mint invites from the UI for any additional
   users.

## Volumes

| Volume        | Holds                                                 |
|---------------|-------------------------------------------------------|
| `torrents`    | Uploaded `.torrent` files and per-torrent sidecars    |
| `auth`        | Passkey credential file (`auth.json`)                 |
| `letsencrypt` | Traefik's ACME state — back this up if you care       |

## Updating

```sh
docker compose pull
docker compose up -d
```

Sessions are in-memory; users will need to log in again after a restart.
Torrents and passkeys persist via the named volumes.

## Notes on security

- The session cookie is `HttpOnly`, `SameSite=Strict`, and (because
  `--http.public-url` is `https://...`) marked `Secure`.
- WebSocket origin is restricted to `EMISSION_HOST` (plus localhost for
  local debugging via SSH tunnel etc.).
- `/docs` is reachable without auth on purpose — the OpenAPI spec leaks
  endpoint shape but no secrets, and API consumers need it browsable.
