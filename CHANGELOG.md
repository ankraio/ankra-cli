# Changelog

All notable changes to the Ankra CLI will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.122] - 2026-02-01

### Added
- **Charts commands** - New commands for browsing and managing Helm charts:
  - `ankra charts list` - List available Helm charts with pagination support
  - `ankra charts search <query>` - Search for charts by name or description
  - `ankra charts info <chart_name>` - Get detailed information about a specific chart including versions and profiles

### Changed
- **CLI login endpoints** - Updated authentication endpoints to use standardized `/api/v1/cli` prefix:
  - Login initialization: `/cli/login/init` → `/api/v1/cli/login/init`
  - Token exchange: `/cli/login/token` → `/api/v1/cli/login/token`
  
This change ensures compatibility with the updated backend API structure where all endpoints use the `/api/v1` prefix.

## [0.1.116-alpha] - 2025-08-18

### Added
- **New `clone` command** - Clone stacks from existing clusters to new cluster configurations
- **URL support for clone** - Clone from HTTP/HTTPS URLs including GitHub raw URLs
- **Smart conflict resolution** - Multiple merge strategies with `--clean`, `--force`, and `--copy-missing` flags
- **Automatic file management** - Downloads and copies referenced files (`from_file`) when cloning
- **Directory structure creation** - Automatically creates necessary directory hierarchies

### Features
- Clone from local cluster files: `ankra clone source.yaml target.yaml`
- Clone from remote URLs: `ankra clone https://github.com/user/repo/raw/main/cluster.yaml local.yaml`
- **`--clean` flag**: Replace all stacks in target cluster with source stacks
- **`--force` flag**: Force merge even when stack/addon/manifest names conflict
- **`--copy-missing` flag**: Copy missing files even from skipped stacks due to conflicts
- Smart conflict detection at stack, manifest, and addon levels
- Support for merging with existing cluster configurations
- Comprehensive clone summary with statistics and next steps

### Enhanced
- Updated README with comprehensive clone command documentation
- Added clone examples to basic workflow and command reference sections
- Enhanced help text and usage examples

### Technical
- HTTP/HTTPS URL parsing and downloading
- Base URL resolution for relative file paths
- Error handling for missing files (404s) with graceful fallback
- File existence checking with optional overwrite functionality

## [0.1.115-alpha] - Previous Release

### Features
- Cluster management and selection
- Operations tracking and insight
- Stack and manifest management
- Addon installation and management
- Platform hooks and automation
- Authentication via API token

---

## Version History

- **0.1.122**: API standardization, OpenAPI documentation, and organisation switch fix
- **0.1.116-alpha**: Added cluster cloning functionality with URL support
- **0.1.115-alpha**: Core platform features
