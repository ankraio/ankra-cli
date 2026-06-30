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

### v0.6.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra cluster ovh ssh-keys <get\|set> <cluster_id>` | v0.4.0 | `ankra cluster ssh-keys <get\|set> <cluster_id>` | The provider is detected automatically from the cluster. The generic group also adds `resync` and works for Hetzner and UpCloud. |

### v0.5.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra cluster hetzner upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | The provider is now detected automatically from the cluster, so users no longer pick a provider namespace. |
| `ankra cluster ovh upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | Same as above. |
| `ankra cluster upcloud upgrade <cluster_id> <target_version>` | v0.4.0 | `ankra cluster upgrade <cluster_id> <target_version>` | Same as above. |
| `ankra cluster {hetzner,ovh,upcloud} scale <cluster_id> <worker_count>` | v0.4.0 | `ankra cluster scale <cluster_id> <worker_count>` | The provider is detected automatically from the cluster. |
| `ankra cluster {hetzner,ovh,upcloud} node-group <list\|add\|scale\|upgrade\|delete> ...` | v0.4.0 | `ankra cluster node-group <list\|add\|scale\|upgrade\|delete> ...` | The provider is detected automatically from the cluster. |
| `ankra cluster {hetzner,ovh,upcloud} deprovision <cluster_id>` | v0.4.0 | `ankra cluster deprovision <cluster_id> [--force]` | The provider is detected automatically; the generic verb also routes to the provider-specific teardown endpoint. |

## Removed

_Nothing removed yet._
