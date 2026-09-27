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
| Room metadata | `30078`, `d=room:<room>` | Requires `name` and an `admin` (valid nostr pubkey), and that the sender pubkey is linked to Hive. The first linked account to publish a given room name becomes its sole owner. |

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
(without `-v`).

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
