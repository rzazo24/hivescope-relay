# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A self-hosted Nostr relay (Go, [khatru](https://github.com/fiatjaf/khatru))
that is deliberately *not* general-purpose: it only accepts three specific
event shapes for a decentralized chat whose identities are linked to real
Hive blockchain accounts. Everything else gets rejected by design (see
`internal/policies/allowlist.go`). Full behavior is documented in
`README.md`/`README.es.md`; this file is about how the code fits together.

The frontend (actual chat UI) intentionally lives in a **separate repo**,
not here — don't add frontend code to this repository unless explicitly
asked to reconsider that.

## Commands

```bash
# Build / vet / unit tests — CGO_ENABLED=1 is required, the sqlite driver
# (mattn/go-sqlite3) uses cgo and needs a C compiler
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=1 go test ./...

# a single test
go test ./internal/policies/ -run TestRoomMetaPolicy_RejectsClaimByDifferentPubkey -v

# run locally (listens on :3334 by default; see README for env vars)
go run .

# end-to-end smoke test against a running relay (Node + nostr-tools)
cd test && npm install && RELAY_URL=ws://localhost:3334 npm test

# full stack via Docker (relay + Caddy reverse proxy with TLS)
docker compose up -d --build

# SQLite backup (safe under concurrent writes, uses sqlite3 .backup, not cp)
./scripts/backup-db.sh
```

CI (`.github/workflows/ci.yml`) runs build/vet/unit tests plus the Node
smoke test against a real built-and-running binary — mirror that when
changing behavior instead of only trusting unit tests in isolation.

## Architecture

**Policy chain is the core of the whole project.** `main.go` wires a
`khatru.Relay` to an `eventstore/sqlite3` backend and registers a chain of
`RejectEvent` policies in `internal/policies/`. khatru evaluates them in
registration order and **stops at the first one that rejects** — this order
is load-bearing, not incidental:

1. `khatrupolicies.EventIPRateLimiter` (khatru's own built-in, not custom
   code) goes **first**, so a flood of forged events gets stopped before it
   can reach the more expensive checks below — in particular before
   `NewHiveLinkPolicy` makes a live outbound HTTP call to a Hive node.
2. `NewHiveLinkPolicy` (`hivelink.go`) — validates `kind:30078 d=hive-link`
   events: verifies `hive_sig` is a real secp256k1 signature (recoverable,
   compact — the same scheme Hive Keychain/hive-tx use) made with the
   account's actual **posting** key, fetched live via `internal/hiveapi`
   (cached 5 min per account) and checked in `internal/hivecrypto`.
3. `NewChatMessagePolicy` (`chatmessage.go`) — validates `kind:9` chat
   messages. Critically, it does **not** re-verify any Hive signature: it
   just queries the relay's own SQLite store (`db.QueryEvents`, passed in
   from `main.go`) for a previously-*accepted* `hive-link` event from the
   same pubkey. There is no separate "verified identities" table — the
   event store itself is the source of truth, because a `hive-link` event
   only ever gets stored if step 2 already accepted it.
4. `NewRoomMetaPolicy` (`roommeta.go`) — validates `kind:30078
   d=room:<name>` events the same way (requires an already-linked pubkey),
   plus first-claim-wins ownership of the room name (queries for an
   existing room event with the same `d` tag from a *different* pubkey).
   Once a room exists, further updates are accepted from either the author
   of the currently-stored (newest) event *or* whoever that event's `admin`
   tag names — this is how a room's creator delegates administration.
   `findRoomOwnership` has to resolve "currently stored" by comparing
   `created_at` (same tie-break as `eventstore/sqlite3.ReplaceEvent`: higher
   id wins), not by taking whichever row a query happens to return last:
   NIP-33 replacement is scoped to `(pubkey, kind, d)`, so once a *different*
   pubkey (a delegated admin) publishes to the same `d`, there are
   legitimately two stored rows for that room — the original creator's and
   the delegate's — each replaceable only within its own author's series.
   Whichever row is newest is authoritative; the other is stale but not
   deleted. A practical consequence: delegating administration transfers
   control forward (the delegate can rename, or delegate further), it does
   **not** leave the original creator with a standing right to reclaim the
   room — they'd need to be re-added to `admin` by whoever currently
   controls it.
5. `NewAllowedEventsPolicy` (`allowlist.go`) — catch-all: anything that
   isn't one of the three shapes above is rejected. This is what makes the
   relay single-purpose instead of a generic open relay; it must stay
   registered. It explicitly lets `kind:5` (NIP-09 deletion) through
   untouched — khatru authorizes deletions with its own separate logic
   (`handleDeleteRequest`, pubkey-matching) *before* this chain runs, but
   since kind:5 isn't an ephemeral kind it *also* gets passed through
   `handleNormal`/`RejectEvent` afterward. If this policy rejected kind:5,
   the deletion would still actually happen but the client would receive a
   false `OK:false` — confirmed in practice while building the frontend
   (the room got deleted from storage despite the relay reporting
   rejection). Don't reintroduce that by narrowing the switch back down to
   just `ChatMessageKind`.

`kinds.go` holds `AppDataKind` (`30078`), shared by both `hive-link` and
`room:*` since they're the same NIP-78 kind distinguished only by their `d`
tag.

`khatrupolicies.FilterIPRateLimiter` / `ConnectionRateLimiter` are wired the
same way onto `RejectFilter` / `RejectConnection`.

**Known, intentional gaps** (explicit product decisions made in
conversation, not oversights — don't "fix" these without asking first):
reading (`REQ`) has zero access control, anyone can query anything stored;
the `admin` tag's only enforced privilege is renaming/editing the room's own
metadata (see `NewRoomMetaPolicy` above) — there's no message-deletion or
kick/ban privilege for admins, that was explicitly scoped out when this
feature was requested; and a single Hive account can link any number of
Nostr pubkeys simultaneously, with no revocation mechanism beyond a
standard NIP-09 delete of the `hive-link` event itself.

**Deployment**: `Dockerfile` is multi-stage (`golang:1.27-bookworm` build
with cgo, `debian:bookworm-slim` runtime). `docker-compose.yml` runs
`relay` with **no published host port** (only reachable from `caddy` over
the internal Docker network) behind `caddy`, which terminates TLS
(Let's Encrypt via `Caddyfile`) and also serves `web/tools/*` as static
files (`handle_path /tools/*`) alongside reverse-proxying everything else
to the relay. Both containers have Docker healthchecks and are capped at
10MB×3 log files. Production runs on the same Oracle Cloud VPS this repo is
usually developed on (arm64/Ampere) at `wss://relay.hivescope.xyz` — it is
not a separate remote target reached only via CI/CD.

`web/tools/*.html` are standalone pages with no build step, served directly
by Caddy rather than published as sandboxed artifacts, because
`hive-link-signer.html` needs to be a genuine top-level page load: Hive
Keychain's mobile in-app browser injects `window.hive_keychain` into the
top frame only, not into cross-origin iframes (which is how sandboxed
artifact previews render). `room-viewer.html` is a read-only debug tool
that opens a native WebSocket to the page's own origin to display a room's
stored events live — neither page is the real frontend.
