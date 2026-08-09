# Security Policy

## Supported versions

OpenModelPool ships as a single binary from the `main` branch. Only the **latest
released version** receives security fixes. There are no long-term support
branches — upgrading is a matter of replacing one file.

| Version | Supported |
|---------|-----------|
| Latest release | ✅ |
| Anything older | ❌ — please upgrade |

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub's
[Security Advisories](https://github.com/lisiyu/openmodelpool/security/advisories/new)
form, which lets us discuss and prepare a fix before disclosure.

Useful things to include:

- What an attacker can achieve, and what access they need to start
- Version (`GET /api/version`) and how the node is deployed (personal / shared / behind a proxy)
- Reproduction steps or a minimal proof of concept
- Whether the issue is reachable from an unauthenticated request

This is a volunteer-run non-profit project, so there is **no bug bounty** and no
guaranteed response time. In practice you should hear back within about a week.
Credit in the release notes is offered unless you prefer to stay anonymous.

## Scope

Most relevant to this project:

- Authentication bypass on the admin plane (`/api/*`, JWT, bcrypt login)
- API key leakage — logs, error responses, config export, the admin UI
- Federation trust issues: forged `X-Node-ID` signatures, replay of relay
  requests, poisoning of the contribution ledger or its hash chain
- Quota-gate bypass that lets one caller monopolise the community free pool
- SSRF or request smuggling through the gateway into upstream providers
- Anything that lets a remote caller read files or run commands on the host

Generally **out of scope**:

- Attacks that need an already-compromised host or admin credentials
- Denial of service through sheer request volume against a self-hosted node —
  rate limiting exists, but a single binary on someone's home server is not
  expected to survive a botnet
- Issues in upstream model providers or in third-party clients
- Missing hardening headers with no demonstrated impact

## What this software does not promise

Being honest about the threat model is more useful than a checklist:

- A node holds **your provider API keys in plaintext config** on the machine you
  run it on. Anyone with filesystem access to `data/` has those keys. Encrypting
  them with a key stored next to them would be theatre, so we do not pretend to.
- Federation trust is **reputational, not cryptographic proof of honesty**. A
  peer that signs correctly can still return garbage responses; the trust score
  and ledger are designed to make that visible over time, not to prevent it.
- **Do not expose the admin plane to the public internet.** Put it behind a
  tunnel, a VPN, or an authenticating reverse proxy.

If you find that the code does something weaker than what the documentation
claims, that is a valid report — the gap between the two is exactly what we care
about.
