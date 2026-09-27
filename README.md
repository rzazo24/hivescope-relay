# hivescope-relay

[![CI](https://github.com/rzazo24/hivescope-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/rzazo24/hivescope-relay/actions/workflows/ci.yml)

*[Leer en español](README.es.md)*

Self-hosted [Nostr](https://nostr.com/) relay written in Go with
[khatru](https://github.com/fiatjaf/khatru), built as the real-time backend
for a decentralized chat linked to [Hive](https://hive.io/) (blockchain)
accounts.

Unlike an off-the-shelf relay (strfry, nostr-rs-relay, etc.), this relay has
its own validation logic: it only accepts the three event types the
HiveScope chat needs, and validates each of them against the Hive blockchain
before storing them.

## What it validates

| Event | kind | Rule |
|---|---|---|
| Hive↔Nostr identity link | `30078`, `d=hive-link` | `hive_sig` must be a real signature, made with the **posting** key of the `hive_account`, over the message `hivescope-relay-link:<nostr_pubkey>`. It's verified against the real posting key, queried live from a Hive node. |
| Chat message | `9` | Requires the `t` tag (room) and that the sender pubkey already has a valid, saved link event. |
| Room metadata | `30078`, `d=room:<room>` | Requires `name` and an `admin` (valid nostr pubkey), and that the sender pubkey is linked to Hive. The first linked account to publish a given room name becomes its owner; from then on, only that room's current owner or the pubkey named in its `admin` tag may publish further updates (rename, or delegate admin to another account). |

Any event that doesn't match one of these three shapes — any other kind, or
a `30078` event with a different `d` — is rejected outright. This relay is
not meant to be a general-purpose Nostr relay; it exists only to back the
HiveScope chat.

It's also rate-limited per IP, using khatru's built-in
[`policies`](https://github.com/fiatjaf/khatru/tree/master/policies)
package: new connections, published events, and `REQ` filters each have
their own limit (see the `khatrupolicies.*RateLimiter` calls in `main.go`
for the exact numbers). The event limiter runs *first*, before any of the
three policies above — so a flood of fake `hive-link` attempts gets stopped
before it can trigger a live call to the Hive API for each one.

One thing this relay does **not** restrict, worth keeping in mind: reading
(subscribing via `REQ`) has no access control — anyone can query anything
that's stored, with no per-room privacy.

Each rule is documented as a comment in its corresponding policy file, under
`internal/policies/`.

## Project structure

```
main.go                          wires up khatru + sqlite and registers the policies
internal/hiveapi/                JSON-RPC client against the public Hive API
internal/hivecrypto/              Hive signature verification (recoverable secp256k1)
internal/policies/               the three validation policies (khatru RejectEvent)
test/test-relay.mjs              Node (nostr-tools) smoke test against a real relay
Dockerfile, docker-compose.yml   build and infrastructure for deployment
Caddyfile                        reverse proxy with automatic TLS (Let's Encrypt)
```

## Running locally (without Docker)

Requires Go 1.27+ and a C compiler (the SQLite driver uses cgo).

```bash
go run .
```

By default it listens on `:3334`, stores its database at
`./data/hivescope-relay.sqlite`, and queries `https://api.hive.blog`. This
can be changed with environment variables:

| Variable | Default | What it does |
|---|---|---|
| `HIVESCOPE_LISTEN_ADDR` | `:3334` | address/port the relay listens on |
| `HIVESCOPE_DB_PATH` | `./data/hivescope-relay.sqlite` | path to the SQLite file |
| `HIVESCOPE_HIVE_NODE` | `https://api.hive.blog` | Hive node used to verify accounts against |
| `HIVESCOPE_SUPERADMIN_HIVE_ACCOUNT` | *(empty, disabled)* | Hive account (case-insensitive) exempt from room ownership checks — can rename/re-delegate any room, not just ones it created or administers. Still no message-deletion or kick/ban privilege. |

## Running the tests

```bash
# Go unit tests (signature verification and policies)
go test ./...

# end-to-end smoke test (needs a relay running)
cd test
npm install
RELAY_URL=ws://localhost:3334 npm test
```

The Node script generates two test keypairs and confirms the relay rejects:
(1) a link event with an invalid hive signature, and (2) a chat message from
a pubkey that never linked an account.

## Deploying with Docker

```bash
docker compose up -d --build
```

This brings up two containers:
- `relay`: the Go binary + SQLite. **Publishes no port to the host** — it's
  only reachable from `caddy`, inside Docker's internal network.
- `caddy`: reverse proxy that exposes `80`/`443` externally and automatically
  obtains a Let's Encrypt TLS certificate for the domain configured in
  `Caddyfile` (`relay.hivescope.xyz` by default — change it there if you use
  a different domain).

SQLite data and Caddy's certificates live in named volumes (`relay-data`,
`caddy-data`, `caddy-config`), so they survive a `docker compose down`
(without `-v`). Both services also cap their Docker logs at 10 MB × 3 files
(`json-file` driver, set in `docker-compose.yml`) — the default is
unbounded, which would otherwise slowly fill up the disk over time.

Both containers are set to `restart: unless-stopped`, and Docker itself is
enabled to start on boot, so a VPS reboot brings everything back up on its
own — as long as `docker.service` is enabled (`systemctl is-enabled
docker`) and the containers weren't manually stopped beforehand.

Both also have a Docker `HEALTHCHECK`: `relay` runs itself with
`--healthcheck` (a tiny built-in mode that just GETs its own NIP-11
endpoint — the final image is debian-slim with no curl/wget, so this avoids
installing extra tools just for that), and `caddy` pings its own admin API
on `127.0.0.1:2019`. `caddy` won't start until `relay` reports healthy
(`depends_on: condition: service_healthy`).

This `caddy` container also doubles as the shared HTTPS entry point for the
whole VPS: since only one process can bind to host port 443, the
[hivescope-web](https://github.com/rzazo24/hivescope-relay-web) frontend
(a separate repo, expected to be checked out as a sibling directory,
`../hivescope-web`) is served from the same Caddy via another site block in
`Caddyfile`, with its `dist/` build mounted in read-only as another volume.
The two projects still have fully independent code and deploy steps —
redeploying the frontend means rebuilding `hivescope-web` and recreating
just this `caddy` container, not touching `relay` at all.

### Before bringing it up on a VPS

1. **DNS**: the domain set in `Caddyfile` must resolve (A/AAAA record) to the
   VPS's public IP *before* Caddy starts — otherwise Let's Encrypt won't be
   able to validate the domain.
2. **VPS firewall** (at the OS level): open `tcp/80` and `tcp/443`. On Ubuntu
   with iptables (no ufw), something like this, inserted before any general
   `REJECT`/`DROP` rule:
   ```bash
   sudo iptables -I INPUT <line> -p tcp -m state --state NEW -m tcp --dport 80 -j ACCEPT
   sudo iptables -I INPUT <line> -p tcp -m state --state NEW -m tcp --dport 443 -j ACCEPT
   sudo netfilter-persistent save   # so it survives a reboot
   ```
3. **Cloud firewall** (if it's Oracle Cloud): besides the OS firewall,
   there's a separate network-level firewall — the VCN's **Security List**
   (or **Network Security Group**) — which by default only allows SSH
   through. Without opening `tcp/80` and `tcp/443` there (source
   `0.0.0.0/0`), Let's Encrypt will never be able to complete validation,
   even if the OS firewall is already open: OCI console → your instance →
   *Attached VNICs* → the VNIC → *Subnet* → *Security Lists* → the default
   list → *Add Ingress Rules*.
4. Only then: `docker compose up -d --build`. You can follow the
   certificate's progress with `docker compose logs -f caddy` — if DNS and
   both firewalls are set up correctly, it usually resolves within seconds.

### Backups

`scripts/backup-db.sh` takes a consistent snapshot of the SQLite database
(using sqlite3's own `.backup` command, safe even while the relay is
writing to it — not a raw file copy) and gzips it. It runs in a throwaway
Alpine container that mounts the same `relay-data` Docker volume read-only,
so it works without needing anything installed on the host besides Docker.

```bash
./scripts/backup-db.sh
```

By default it writes to `~/backups/hivescope-relay/` and deletes backups
older than 14 days; both are configurable via `BACKUP_DIR` and
`RETENTION_DAYS` env vars. On the production VPS it's scheduled daily via
cron:

```
17 3 * * * /path/to/hivescope-relay/scripts/backup-db.sh >> ~/backups/hivescope-relay/backup.log 2>&1
```

To restore, stop the relay, gunzip a backup over the volume's database file,
and start it again:

```bash
docker compose stop relay
gunzip -c ~/backups/hivescope-relay/hivescope-relay-<timestamp>.sqlite.gz \
  | docker run --rm -i -v nostr-relay_relay-data:/data alpine:3.20 sh -c 'cat > /data/hivescope-relay.sqlite'
docker compose start relay
```

## Linking a Hive account (for the frontend)

A client needs to publish a `kind:30078` event like this:

```json
{
  "kind": 30078,
  "tags": [
    ["d", "hive-link"],
    ["hive_account", "<hive_username>"],
    ["hive_sig", "<hex_signature>"],
    ["hive_key_type", "posting"]
  ],
  "content": ""
}
```

Where `hive_sig` is the result of signing, **with that account's posting
key** (e.g. via `hive_keychain.requestSignBuffer`), exactly the string:

```
hivescope-relay-link:<nostr_pubkey_of_the_event>
```

(the pubkey of the event being published itself, in hex). See
`internal/policies/hivelink.go` (the `LinkChallenge` function) for the exact
details — it's a contract between the frontend and the relay, and can't be
changed on one side without the other.

## License

[MIT](LICENSE)
