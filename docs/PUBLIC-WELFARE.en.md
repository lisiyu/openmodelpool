# OpenModelPool — A Public-Welfare-First LLM API Aggregation Gateway

> A note on *why it is worth running a node / why it is worth contributing compute*. Everything described here maps to already-shipped code — no exaggeration, no vaporware.

## What it is

OpenModelPool is an **open-source, single-binary, self-hosted** LLM API aggregation gateway:

- Speaks both **OpenAI `/v1/chat/completions`** and **Anthropic `/v1/messages`**. Existing SDKs (OpenAI / Anthropic / LangChain, etc.) just point their base URL at it — no business-code changes.
- Built-in **load balancing + smart routing + failover** (picks the best node by score; auto-retries the next healthy channel on upstream failure).
- Built-in **per-API-key quota management** plus a **public-welfare quota allocation engine** (Guest Key Pool / Public Key Pool ratios are tunable).

## Public-welfare first, no business model

This is the fundamental divergence from同类 projects:

- **No points, no tokens, no skim.** There is no tradable token economy of any kind.
- Node operators split their own compute — at ratios they set — between a "guest pool" and a "public-welfare pool" for the community to use for free.
- The ledger is publicly auditable: anyone can verify *where compute came from and where it went*.

Versus commercial gateways: LiteLLM / One API / New API are feature-mature but carry commercial tiers, online payments, or enterprise paywalls; OpenRouter takes a 5.5% platform fee on card spend. OpenModelPool is **zero-fee, zero-token, open-ledger** end to end.

## Decentralized federation (censorship-resistant / perpetual)

- Nodes establish direct channels via **STUN / NAT-type probing + UDP hole-punching**, and discover each other via **DHT (Kademlia)** — no single central dependency.
- The contribution ledger is **replicated across federation nodes**, content-hash (sha256) anchored. A background **auto-reconcile every 60s** heals missing records and only *warns* (never overwrites) on divergence — the loss of any single node cannot make the ledger disappear.

## Transparency (trust through auditability)

- Contribution records are content-hash anchored (`ContentHashStore`, `sha256:` prefix); node-specific signatures are stripped before cross-replica digest comparison.
- `GET /api/admin/ledger/transparency`: aggregates *which compute was contributed* per node / per model, and verifies the transaction-chain integrity.
- Contribution details and the full ledger can be **exported as CSV / JSON** (`GET /api/admin/ledger/export`) for researchers.

## Public-welfare quota loop (contribution → free quota)

This is the key piece that makes "decentralization" actually run (P2-3):

- When a node donates compute to the public-welfare pool, its contribution accrues **1:1 as a "free-quota entitlement"** — **zero fee, no inflation, non-tradable**, just transparent bookkeeping: "contributor X donated N tokens → is entitled to use N tokens from the public pool for free".
- `GET /api/admin/ledger/contribution-quota`: every contributor's "contributed ↔ earned free quota" is always queryable.
- Consumer-side deduction by contributor identity (P2-3(ii)) will be added incrementally once the user/identity model is in place.

> This is kin to Petals' "contribute GPU, earn community acceleration / perks" — but at the API-gateway layer, and fully public-welfare, no currency.

## Architecture in one line

```
Gateway layer   compatible endpoints + routing/load-balance/failover + per-key quota
Federation layer DHT discovery + NAT traversal/direct link + cross-node ledger replication & reconciliation
Ledger layer     content-hash anchoring + multi-replica redundancy + self-healing + public-welfare quota loop
```

## How to run (one-line deploy)

```bash
docker compose up -d        # data persisted in named volume omp-data, listens on :8000 by default
docker compose logs -f      # view logs
```

For public exposure, prefer a **Cloudflare Tunnel / your own reverse proxy** over exposing port 8000 directly. See `docker-compose.yml` and existing deploy docs.

## Free pool, out of the box (any node + a hard-coded public key)

OpenModelPool ships a **community free pool**: a batch of anonymous, key-less free LLM APIs (auto-synced from the `awesome-free-llm-apis` list, on by default). Anyone can use it **without registration and without their own API key** — they only need:

- **the address of any OpenModelPool node** (e.g. `https://openmodelpool.io`), plus
- the **globally hard-coded public key** `sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1`

Point your base URL at the node:

```bash
curl https://<any-node>/v1/chat/completions \
  -H "Authorization: Bearer sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1" \
  -H "Content-Type: application/json" \
  -d '{"model":"<model>","messages":[{"role":"user","content":"hello"}]}'
```

### Ownership model of the free quota (personal use vs public pool)

This is the core operating model, and what sets it apart from commercial gateways:

- **Everyone gets free quota out of the box**: install OMP with free-pool auto-sync on (default `free_pool_auto_sync=true`) and you **automatically get model access from the preset free upstream** — **you can use it freely even if you added no upstream of your own**.
- **Sharing is not forced**: by default a node runs in **personal mode**; your free quota (and your own upstream quota) is **for your own consumption only, not in the federation public pool**. The system **never forces** you to join the shared network.
- **Joining turns it into a public pool**: when you **voluntarily join the shared network**, your free upstream and your own quota become **public-pool resources**, giving the community more egress and more concurrent quota — because free providers usually cap concurrency and total quota, **more egress means more available resources**.
- **No quota to contribute? Be a gateway**: even if you have no quota to contribute, after joining the shared network you can still **forward traffic and act as a gateway / egress node** — that is also part of community governance; egress itself is a contribution.

Key points:

- **Any node works**: the free pool is a community commons, not bound to any node. Even in personal mode, a public key only reaches the "community free pool" and **never exposes the operator's own private paid channels** — privacy and public welfare are separated here.
- **Models are queryable anytime**: `GET /v1/models` (with the same public key) returns the currently synced free-pool model list (dynamic with the upstream list).
- **Guard against abuse, not against people**: per the governance philosophy "assume goodwill by default, only guard against malicious abuse", public-key requests get **per-client-IP four-tier quota limiting** (global daily / per-IP daily / hourly window / per-model) — stopping one abuser from monopolizing the free pool while normal users are barely aware of it.
- Want stronger models or higher quotas? Drop your own provider key into the node; it will automatically split your "spare quota" into the public-welfare pool by ratio — that is exactly how it runs.

## Community self-governance (contributor governance)

Node admission, model allow-lists, parameter tuning — public decisions are made by **contributor governance**: voting eligibility belongs to nodes that have *contributed compute to the commons* ("contributors govern"). Proposals and ratifications are written into an **append-only, hash-chained governance ledger** whose whole history is verifiable and tamper-evident; a proposal passes with a **supermajority (≥ 2/3 of eligible voters)**.

The governance philosophy is **"assume goodwill by default"**: the system only guards against **proposal spam** (at most 5 open proposals per node), and does **no punishment, no scoring, no trust scoring** — dissent is not liquidated, it just fails to reach the threshold. This is a sharp contrast to commercial platforms' "stake / slash" governance models.

- **Joining is never forced**: by default a node is in personal mode; the system **never forces** you to join the shared network. Only when a node still has **idle own quota** does it emit a **gentle soft reminder** (in the startup log and the `/api/network/status` status endpoint) — encouraging, not coercing — suggesting you contribute idle quota to the community public pool. Not joining is perfectly usable too (personal use + community free pool only).
- **Joining is additive, not an obligation**: joining the shared network brings more egress, more concurrent quota, and unlocks the "contribution → public-welfare quota" loop; but not joining — or even having no own quota at all — you can still participate as a gateway forwarding traffic.

## Still in progress (honest notes)

- The direct UDP channel currently carries **signaling and link setup**; actual request payload over the self-built UDP protocol is follow-up work (P2).
- Consumer-side deduction of public-welfare quota by identity (P2-3(ii)) will be added incrementally once the user/identity model is in place.
- **Shipped (2026-08-09)**: free-quota ownership model (personal vs public pool), non-forced join with soft reminder, and gateway/forwarding role — see the "Ownership model of the free quota" and "Community self-governance" sections above.
