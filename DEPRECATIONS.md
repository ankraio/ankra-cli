# Ankra CLI Deprecations

This file tracks Ankra CLI features and commands that are **deprecated and
scheduled for removal**. Deprecated commands keep working until their removal
version; running one prints a warning pointing at the replacement.

## Policy

- Deprecations are only introduced in a **minor or major** release, never in a
  patch.
- A feature stays deprecated for **at least one minor version** before it is
  removed, so there is always an upgrade path on a stable release.
- Removals only happen on a **minor or major** version bump, never in a patch.
- Each deprecation is announced in `CHANGELOG.md` and surfaced at runtime via
  the command's deprecation warning.

## Upcoming removals

### v0.5.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra cluster hetzner upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | The provider is now detected automatically from the cluster, so users no longer pick a provider namespace. |
| `ankra cluster ovh upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | Same as above. |
| `ankra cluster upcloud upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | Same as above. |

## Removed

_Nothing removed yet._
