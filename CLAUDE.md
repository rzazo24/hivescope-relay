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

# self-heal check (cron, every 2 min): pings the chat and the relay's NIP-11
# over HTTPS and restarts caddy/relay after 2 consecutive failures (10 min
# cooldown); log in ~/backups/hivescope-relay/healthcheck.log. It cannot alert
# anyone, and can't help if the whole machine is down.
./scripts/healthcheck.sh

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
   messages (content capped at `MaxChatMessageLength` = 2000 *characters* — runes, not bytes; the frontend mirrors it as `MAX_MESSAGE_LENGTH`). Critically, it does **not** re-verify any Hive signature: it
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
   controls it. `HIVESCOPE_SUPERADMIN_HIVE_ACCOUNT` (env var, empty =
   disabled; set to `rzazo24` in `docker-compose.yml`) names a single Hive
   account exempt from the ownership check entirely — that account can
   rename/re-delegate *any* room, not just ones it created or was delegated.
   It's matched against `findLinkedHiveAccount`'s result (case-insensitively,
   via `strings.EqualFold`), not the Nostr pubkey, specifically because a
   Hive account can be linked from several different Nostr pubkeys/devices —
   pinning this to one pubkey would break as soon as that operator logged in
   from a new browser. Still requires the usual hive-link (rule 2); doesn't
   grant message-deletion or kick/ban either, same scope as ordinary admins.
   **Ownership is really per Hive account, not per pubkey**: every
   browser/device generates its own Nostr pubkey, so a pubkey that isn't the
   owner/admin but is linked to the *same Hive account* as the owner's or
   admin's pubkey is also accepted (`sharesHiveAccount`, via
   `findLinkedHiveAccount`, case-insensitive). Without that, creating a room
   on a phone locked you out of it on your PC. It's only checked after the
   cheap exact-pubkey/superadmin checks fail. The frontend mirrors the rule
   in `canManageRoom` (cosmetic; the relay decides).
   **Deleting messages is per Hive account too** (`deletion.go`,
   `NewDeletionOutcome`, registered on khatru's `OverwriteDeletionOutcome`).
   khatru's default is "same pubkey only", but when an overwrite function
   exists khatru uses *its* result instead of that rule, so it re-implements it:
   same pubkey always; and, for `kind:9` chat messages only, another pubkey
   linked to the same Hive account. Not extended to other kinds on purpose —
   otherwise one device could delete another device's `hive-link` or a room.
   Denies on any lookup error or missing link.
   **Reactions** (`reaction.go`, `ReactionKind` = 7, NIP-25): content must be one
   of the closed `ReactionEmojis` list (the frontend's `REACTION_EMOJIS` must
   match), tags exactly one `e` (message id), `p` (its author) and `t` (room);
   the target `kind:9` must exist and its author/room must match the tags, the
   pubkey must be linked, and the same pubkey can't repeat the same emoji on
   the same message. The policy re-checks ids/kinds/tags of what the store
   returns instead of trusting the filter. Removing a reaction is a NIP-09
   delete, and `NewDeletionOutcome` gives kind 7 the same per-Hive-account rule
   as kind 9. Reactions have their own IP rate limiter (`SplitRateLimitKind`,
   20/min burst 40) so they don't eat the chat's. `roomsweep` deletes reactions
   whose room is gone or whose message no longer exists.
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

**Presence** (`presence.go`, `PresenceKind` = 20078, an *ephemeral* kind —
khatru relays it to subscribers and stores nothing): each linked client
publishes a heartbeat every ~25 s with a `t` tag = the room it's in (none =
the room list) and a `left` tag on the way out; clients count distinct Hive
accounts with a fresh beat. `NewPresencePolicy` requires a verified hive-link
(so "online" means real linked accounts), empty content, only those tags, and
`created_at` within ±120 s of the relay clock. A `typing` tag (only valid
together with `t`) marks the beat as "is typing in this room" — that's the whole
"is typing…" feature, the relay treats it as one more presence beat (same
limiter, so the frontend throttles it to one per 4 s). Heartbeats get their **own IP
rate limiter** (`SplitRateLimit`, 30/min burst 60) — sharing the chat one (5/min)
would let a couple of tabs behind one IP starve real events. Two khatru
quirks: an ephemeral event nobody is subscribed to answers `OK false "mute: no
one was listening"` (harmless: the sender always listens to its own), and the
relay stores/keeps no history, so a newcomer only learns who's online from
live beats — the frontend solves that with a short randomized "welcome beat"
reply, not with any relay support.

`kinds.go` holds `AppDataKind` (`30078`), shared by both `hive-link` and
`room:*` since they're the same NIP-78 kind distinguished only by their `d`
tag.

`khatrupolicies.FilterIPRateLimiter` / `ConnectionRateLimiter` are wired the
same way onto `RejectFilter` / `RejectConnection`. The filter (REQ) limiter is 60/min, burst 180 per IP: the
frontend now shares one connection but still makes ~10 REQs per page load, and
the old 20/min / burst 60 locked people out after ~6 reloads.

**Room expiration and cleanup**: room-metadata events must carry a NIP-40
`expiration` tag (validated in `NewRoomMetaPolicy`, required, future unix
timestamp). Since the metadata event is NIP-33 replaceable, any update
(rename, delegate admin, or the frontend's "edit" even with no real change)
republishes a fresh `expiration` — that's the whole "renew a room"
mechanism, there's no renew endpoint or button.

**Don't rely on khatru's own NIP-40 sweep.** It exists (`expirationManager`,
hourly) but in production it left a room 20 h past its expiration
undeleted; it also only tracks what was published since the last start and
its countdown resets on every deploy. `internal/roomsweep` enforces
expiration itself instead, at startup and every 5 minutes, and does three
things in one pass over the stored events:

1. **Prunes superseded room rows.** NIP-33 replacement is scoped to
   `(pubkey, kind, d)` and every browser/device has its own Nostr pubkey, so
   editing a room from another device leaves *two* rows for the same `d`.
   Only the newest (`created_at`, higher id on a tie) is the room; the rest
   are deleted. Without this, an old row — e.g. one from before
   `expiration` existed — outlived every later edit and kept the room alive
   forever (this was the reported bug: "I set a duration on General and it
   never got deleted"). `main.go` also calls `PruneSuperseded` right after
   each accepted room publish (a second `ReplaceEvent` hook registered after
   `db.ReplaceEvent`) so the cleanup is immediate, not up to 5 minutes late.
2. **Deletes the newest row if its `expiration` has passed.** Rooms with no
   `expiration` at all (created before the tag was required) never expire
   until someone edits them.
3. **Deletes orphaned `kind:9` messages**: those whose `t` tag matches no
   live room. It's blind to *why* a room vanished and to any timestamp, only
   "does this room exist now", so a renewed room never loses messages.

Consequence for ownership: since old rows are now deleted, the newest
publisher is the only row left — same rule `findRoomOwnership` already
applied, it just no longer has stale rows to ignore.

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
