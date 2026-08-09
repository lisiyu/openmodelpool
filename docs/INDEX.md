# Documentation Index

> Single entry point for OpenModelPool Agent documentation. The homepage [README](../README.md) is a short summary; everything else lives here.

## For users & operators

| Topic | File |
|-------|------|
| **Getting started / deployment / build** | [DEPLOYMENT_GUIDE.md](DEPLOYMENT_GUIDE.md) |
| **Configuration & encryption** | [CONFIGURATION.md](CONFIGURATION.md) |
| **Full feature catalog** | [FEATURES.md](FEATURES.md) |
| **API reference (proxy + management)** | [API.md](API.md) |
| **Preset platforms & non-OpenAI config** | [PLATFORMS.md](PLATFORMS.md) |
| **Federation / network mode** | [FEDERATION.md](FEDERATION.md) |
| **Access control & security** | [ACCESS_CONTROL.md](ACCESS_CONTROL.md) |
| **Performance optimization** | [PERFORMANCE_OPTIMIZATION.md](PERFORMANCE_OPTIMIZATION.md) |
| **Philosophy & public welfare** | [PUBLIC-WELFARE.md](PUBLIC-WELFARE.md) · [PUBLIC-WELFARE.en.md](PUBLIC-WELFARE.en.md) |
| **Release & promotion kit** | [LAUNCH-KIT.md](LAUNCH-KIT.md) |
| **Changelog** | [CHANGELOG.md](CHANGELOG.md) |

## For contributors

| Topic | File |
|-------|------|
| **Public roadmap (claim work here)** | [BACKLOG.md](BACKLOG.md) |
| **Contributing guide** | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| **Security policy & threat model** | [SECURITY.md](../SECURITY.md) |
| **Issue / PR templates** | [.github/ISSUE_TEMPLATE/](../.github/ISSUE_TEMPLATE) · [.github/PULL_REQUEST_TEMPLATE.md](../.github/PULL_REQUEST_TEMPLATE.md) |

## Internal design specs & historical working docs

> Architecture deep-dives, feature PRDs/ARCHs, test plans and review reports. Kept separate from user docs so the index above stays navigable.

| Topic | File |
|-------|------|
| Master design spec (canonical architecture) | [reference/openmodelpool-v4-design.md](reference/openmodelpool-v4-design.md) |
| Phase-1 architecture slice | [reference/ARCH-phase1-slice1.md](reference/ARCH-phase1-slice1.md) |
| Phase-1 PRD | [reference/PRD-phase1.md](reference/PRD-phase1.md) |
| Federation v4.1.6 — PRD / ARCH | [reference/PRD-federation-v4.1.6.md](reference/PRD-federation-v4.1.6.md) · [reference/ARCH-federation-v4.1.6.md](reference/ARCH-federation-v4.1.6.md) |
| Discovery v4.1.7 — PRD / ARCH / testplan | [reference/PRD-discovery-v4.1.7.md](reference/PRD-discovery-v4.1.7.md) · [reference/ARCH-discovery-v4.1.7.md](reference/ARCH-discovery-v4.1.7.md) · [reference/discovery-v4.1.7-testplan.md](reference/discovery-v4.1.7-testplan.md) |
| Federation testplan | [reference/federation-v4.1.6-testplan.md](reference/federation-v4.1.6-testplan.md) |
| Auto-update — PRD / ARCH | [reference/PRD-auto-update.md](reference/PRD-auto-update.md) · [reference/ARCH-auto-update.md](reference/ARCH-auto-update.md) |
| Domain-binding PRD | [reference/PRD-domain-binding.md](reference/PRD-domain-binding.md) |
| Auto-update diagrams (mermaid) | [reference/auto-update-class.mermaid](reference/auto-update-class.mermaid) · [reference/auto-update-sequence.mermaid](reference/auto-update-sequence.mermaid) |
| Code review report (2026-08-02) | [reference/REVIEW_REPORT.md](reference/REVIEW_REPORT.md) |
| Honest review (2026-08-08) | [reference/REVIEW-2026-08-08.md](reference/REVIEW-2026-08-08.md) |
| Progress log (2026-08-09) | [reference/PROGRESS-2026-08-09.md](reference/PROGRESS-2026-08-09.md) |
| v4.0.1 update summary | [reference/v4.0.1_update_summary.md](reference/v4.0.1_update_summary.md) |
| Share page (html) | [reference/share.html](reference/share.html) |

---

### How this tree was organized

- **`docs/` root** = documentation for users, operators and contributors (kept short and navigable).
- **`docs/reference/`** = internal design specs, feature PRDs/ARCHs, test plans and historical review reports — the working drafts that overlapped the master design spec. They are preserved for traceability but do not clutter the user-facing index.
- The README is intentionally under ~200 lines: it states what the project is, the honest implementation status, a feature summary, quick start, and pointers — full detail lives in the files above.
