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

### v0.15.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra skills <list\|install\|uninstall> --editor <name>` | v0.14.0 | `--client <name>` | `--client` takes a repeatable, comma-separated list plus `all` and `auto`, and reaches every supported assistant rather than only Cursor and Claude Code. `--editor` still resolves as an alias and warns. |
| `ankra skills <list\|install\|uninstall> --personal` | v0.14.0 | *(nothing — it is the default)* | A home-directory install is what happens without `--project`; the flag never did anything else. |

### v0.10.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra cluster deprovision --auto-delete` | v0.9.0 | `ankra delete cluster <name>` after the deprovision completes | The backend parses and discards `auto_delete`, so the flag has never done anything. The flag is now hidden and warns when used; the CLI no longer sends the parameter. |
| `ankra profile auth passkeys ...` | v0.9.0 | `ankra profile auth open` | Passkeys and all other two-factor settings are managed in the browser (passkey enrollment needs a WebAuthn ceremony a terminal cannot run). The forwarder opens Profile Authentication in the browser; the sibling API-backed `profile auth` and `org` RBAC commands were removed outright because they never worked (see CHANGELOG). |

### v0.6.0

| Deprecated | Deprecated in | Replacement | Notes |
|---|---|---|---|
| `ankra cluster ovh ssh-keys <get\|set> <cluster_id>` | v0.4.0 | `ankra cluster ssh-keys <get\|set> <cluster_id>` | The provider is detected automatically from the cluster. The generic group also adds `resync` and works for Hetzner and UpCloud. |
| `ankra cluster ovh node-group <labels\|taints> <cluster_id> <group_name>` | v0.5.0 | `ankra cluster node-group <labels\|taints> <cluster_id> <group_name>` | The provider is detected automatically from the cluster; the generic verbs work for all six providers. |

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
