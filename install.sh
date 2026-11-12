#!/bin/sh
# Ankra CLI installer for macOS and Linux.
#
#   curl -fsSL https://ankra.ai/install | sh
#
# Installs the `ankra` command and then runs `ankra onboard`, which signs you
# up or in and sets up only what your account still needs. To install without
# onboarding:
#
#   curl -fsSL https://ankra.ai/install | sh -s -- --no-onboard
#
# Options (each has an environment variable that does the same):
#   --version vX.Y.Z    install this release instead of the latest  ANKRA_VERSION
#   --install-dir DIR   install into DIR                             ANKRA_INSTALL_DIR
#   --no-onboard        install only, do not run `ankra onboard`     ANKRA_NO_ONBOARD=1
#   --no-modify-path    do not edit shell startup files              ANKRA_NO_MODIFY_PATH=1
#
# What it does:
#   - downloads ankra-cli-<os>-<arch> and its .sha256 from the GitHub release
#     and refuses to install when the checksum is missing or does not match
#   - never asks a question and never uses sudo: it installs next to an ankra
#     you already have when it can write there, else into /usr/local/bin when
#     that is writable, else into ~/.ankra/bin
#   - leaves an ankra managed by Homebrew alone (update it with brew)
#   - adds ~/.ankra/bin to PATH in your shell startup files, inside a block
#     marked "ankra installer" that a rerun replaces rather than repeats
#
# On Windows, use PowerShell instead:
#
#   irm https://ankra.ai/install.ps1 | iex
#
# Test hooks: ANKRA_DOWNLOAD_BASE replaces the release download URL (a file://
# URL works) and ANKRA_INSTALL_SYSTEM_DIR replaces /usr/local/bin.
#
# Everything runs from main at the bottom, so a download cut short runs
# nothing.

set -eu

ANKRA_RELEASES_URL="https://github.com/ankraio/ankra-cli/releases"
PATH_BLOCK_START="# >>> ankra installer >>>"
PATH_BLOCK_END="# <<< ankra installer <<<"

workdir=""
partial_target=""

say() {
	printf '%s\n' "$*"
}

warn() {
	printf 'ankra installer: %s\n' "$*" >&2
}

fail() {
	printf 'ankra installer: error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Install the Ankra CLI, then run `ankra onboard` to sign up or in and set up.

Usage:
  curl -fsSL https://ankra.ai/install | sh
  curl -fsSL https://ankra.ai/install | sh -s -- [options]

Options:
  --version vX.Y.Z    install this release instead of the latest (ANKRA_VERSION)
  --install-dir DIR   install into DIR (ANKRA_INSTALL_DIR)
  --no-onboard        install only, do not run `ankra onboard` (ANKRA_NO_ONBOARD=1)
  --no-modify-path    do not edit shell startup files (ANKRA_NO_MODIFY_PATH=1)
  -h, --help          show this help
EOF
}

is_true() {
	case "${1:-}" in
	1 | true | TRUE | True | yes | YES | Yes | on | ON) return 0 ;;
	*) return 1 ;;
	esac
}

cleanup() {
	if [ -n "$partial_target" ]; then
		rm -f "$partial_target"
	fi
	if [ -n "$workdir" ]; then
		rm -rf "$workdir"
	fi
}

# resolve_path follows symbolic links to the file they end at.
resolve_path() {
	resolved=$1
	hops=0
	while [ -L "$resolved" ] && [ "$hops" -lt 40 ]; do
		link=$(readlink "$resolved") || break
		case "$link" in
		/*) resolved=$link ;;
		*) resolved=$(dirname "$resolved")/$link ;;
		esac
		hops=$((hops + 1))
	done
	printf '%s\n' "$resolved"
}

# is_homebrew_managed matches what `ankra upgrade` checks: the binary lives
# in a Homebrew Cellar or Caskroom, usually reached through a bin/ symlink.
is_homebrew_managed() {
	case "$(resolve_path "$1")" in
	*/Cellar/* | */Caskroom/*) return 0 ;;
	*) return 1 ;;
	esac
}

directory_is_writable() {
	[ -d "$1" ] && [ -w "$1" ]
}

on_path() {
	case ":${PATH}:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

# home_relative writes a path under $HOME as "$HOME/..." so the PATH line
# keeps working if the home directory moves.
home_relative() {
	home=${HOME:-}
	case "$1" in
	"") printf '\n' ;;
	"$home") printf '%s\n' "${home:+\$HOME}" ;;
	"$home"/*) printf '%s\n' "\$HOME/${1#"$home"/}" ;;
	*) printf '%s\n' "$1" ;;
	esac
}

# display_path shows a path under $HOME as ~/... in messages.
display_path() {
	home=${HOME:-}
	if [ -n "$home" ]; then
		case "$1" in
		"$home"/*)
			# shellcheck disable=SC2088 # a literal ~ for the reader
			printf '~/%s\n' "${1#"$home"/}"
			return 0
			;;
		esac
	fi
	printf '%s\n' "$1"
}

download() {
	url=$1
	destination=$2
	show_progress=${3:-}
	if command -v curl >/dev/null 2>&1; then
		set -- --fail --location --retry 3 --output "$destination"
		case "$url" in
		https://*) set -- "$@" --proto '=https' --tlsv1.2 ;;
		esac
		if [ -n "$show_progress" ] && [ -t 2 ]; then
			set -- "$@" --progress-bar
		else
			set -- "$@" --silent --show-error
		fi
		curl "$@" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$destination" "$url"
	else
		fail "downloading needs curl or wget, and neither is installed"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{ print $NF }'
	fi
}

# verify_checksum refuses a checksum file that is missing, empty, not a
# SHA-256 or different from the binary's.
verify_checksum() {
	binary=$1
	checksum_file=$2
	checksum_url=$3
	expected=$(awk 'NR == 1 { print $1 }' "$checksum_file" | tr 'ABCDEF' 'abcdef')
	case "$expected" in
	"" | *[!0-9a-f]*)
		fail "the checksum file at $checksum_url is not a SHA-256 checksum, so the download cannot be verified; refusing to install"
		;;
	esac
	if [ "${#expected}" -ne 64 ]; then
		fail "the checksum file at $checksum_url is not a SHA-256 checksum, so the download cannot be verified; refusing to install"
	fi
	actual=$(sha256_of "$binary")
	if [ -z "$actual" ]; then
		fail "checking the download needs sha256sum, shasum or openssl, and none of them is installed"
	fi
	if [ "$actual" != "$expected" ]; then
		fail "checksum mismatch for the downloaded binary (expected $expected, got $actual); refusing to install"
	fi
}

# path_block is what goes into a shell startup file. It only adds the
# directory when it is not on PATH yet, so a shell that reads two of the
# files does not get it twice.
# shellcheck disable=SC2016 # ${PATH} is written for the startup file to expand
path_block() {
	directory=$(home_relative "$1")
	printf '%s\n' "$PATH_BLOCK_START"
	printf '%s\n' "# Added by the Ankra CLI installer (https://ankra.ai/install). Delete this block to undo it."
	printf 'case ":${PATH}:" in *":%s:"*) ;; *) export PATH="%s:${PATH}" ;; esac\n' "$directory" "$directory"
	printf '%s\n' "$PATH_BLOCK_END"
}

# write_path_block puts the block at the top of a startup file, after taking
# out the one an earlier run wrote. The file is rewritten in place, so its
# permissions and a symbolic link to it (a dotfiles repository) survive.
write_path_block() {
	file=$1
	block=$2
	staged="$workdir/startup-file"
	{
		printf '%s\n' "$block"
		if [ -f "$file" ]; then
			awk -v start="$PATH_BLOCK_START" -v end="$PATH_BLOCK_END" '
				$0 == start { skipping = 1; next }
				skipping && $0 == end { skipping = 0; next }
				!skipping { print }
			' "$file"
		fi
	} >"$staged" || return 1
	cat "$staged" >"$file" || return 1
}

# startup_files lists the files the user's login shell reads.
startup_files() {
	shell_name=$(basename "${SHELL:-sh}")
	case "$shell_name" in
	zsh)
		zsh_directory=${ZDOTDIR:-$HOME}
		printf '%s\n' "$zsh_directory/.zshrc" "$zsh_directory/.zshenv"
		;;
	bash)
		printf '%s\n' "$HOME/.bashrc"
		if [ -f "$HOME/.bash_profile" ]; then
			printf '%s\n' "$HOME/.bash_profile"
		elif [ -f "$HOME/.bash_login" ]; then
			printf '%s\n' "$HOME/.bash_login"
		else
			printf '%s\n' "$HOME/.profile"
		fi
		;;
	*)
		printf '%s\n' "$HOME/.profile"
		;;
	esac
}

# add_to_path writes the PATH block for directory and reports the files it
# changed in path_files_changed.
add_to_path() {
	directory=$1
	path_files_changed=""
	if [ -z "${HOME:-}" ]; then
		warn "HOME is not set, so no shell startup file was changed; add $directory to PATH yourself"
		return 0
	fi
	case "$directory" in
	*'"'* | *'`'* | *'$'* | *\\*)
		warn "not adding $directory to PATH: its name has characters a startup file cannot hold safely"
		return 0
		;;
	esac
	block=$(path_block "$directory")
	files=$(startup_files)
	old_ifs=$IFS
	IFS='
'
	for file in $files; do
		IFS=$old_ifs
		if write_path_block "$file" "$block" 2>/dev/null; then
			path_files_changed="${path_files_changed:+$path_files_changed, }$(display_path "$file")"
		else
			warn "could not update $file; add $directory to PATH yourself"
		fi
	done
	IFS=$old_ifs
}

# link_local_bin makes ~/.local/bin/ankra point at the installed binary, so
# shells that already have ~/.local/bin on PATH find it without a restart.
# A file or a link to anything else is left alone.
link_local_bin() {
	target=$1
	local_bin="$HOME/.local/bin"
	link="$local_bin/ankra"
	if [ -L "$link" ]; then
		if [ "$(readlink "$link")" = "$target" ]; then
			local_bin_linked=1
		fi
		return 0
	fi
	if [ -e "$link" ]; then
		return 0
	fi
	if mkdir -p "$local_bin" 2>/dev/null && ln -s "$target" "$link" 2>/dev/null; then
		local_bin_linked=1
	fi
}

# note_other_copies names every other ankra on PATH. One that comes first
# runs instead of the new install; one the user cannot write to stays stale.
note_other_copies() {
	installed=$(resolve_path "$1")
	install_directory=$(dirname "$1")
	# Only a copy ahead of the install directory on the current PATH is
	# known to run instead; when the directory is not on PATH yet, the
	# startup files put it first in new shells.
	passed_install_directory=0
	if ! on_path "$install_directory"; then
		passed_install_directory=1
	fi
	old_ifs=$IFS
	IFS=:
	set -f
	for directory in $PATH; do
		IFS=$old_ifs
		if [ "$directory" = "$install_directory" ]; then
			passed_install_directory=1
			continue
		fi
		[ -n "$directory" ] || continue
		candidate="$directory/ankra"
		if [ ! -f "$candidate" ] || [ ! -x "$candidate" ]; then
			continue
		fi
		[ "$(resolve_path "$candidate")" != "$installed" ] || continue
		if is_homebrew_managed "$candidate"; then
			advice="remove it with: brew uninstall ankra"
		elif directory_is_writable "$directory"; then
			advice="remove it with: rm $candidate"
		else
			advice="you cannot update it without administrator rights; remove it with: sudo rm $candidate"
		fi
		if [ "$passed_install_directory" = 0 ]; then
			say "Note: another ankra at $candidate comes first on your PATH and runs instead of this one; $advice"
		else
			say "Note: another ankra is installed at $candidate; $advice"
		fi
	done
	set +f
	IFS=$old_ifs
}

detect_os() {
	kernel=$(uname -s 2>/dev/null || echo unknown)
	case "$kernel" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		printf '%s\n' "On Windows, install the Ankra CLI from PowerShell:" >&2
		printf '%s\n' "" >&2
		printf '%s\n' "  irm https://ankra.ai/install.ps1 | iex" >&2
		exit 1
		;;
	*)
		fail "this installer is for macOS and Linux, not $kernel; download a binary from $ANKRA_RELEASES_URL"
		;;
	esac
}

detect_arch() {
	machine=$(uname -m 2>/dev/null || echo unknown)
	case "$machine" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "the Ankra CLI is built for amd64 and arm64 processors, not $machine; see $ANKRA_RELEASES_URL" ;;
	esac
	# A shell running under Rosetta reports x86_64 on an Apple silicon Mac;
	# the native binary is the one to install there.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ]; then
		if [ "$(sysctl -in sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
			arch=arm64
		fi
	fi
}

# run_onboarding hands the terminal to `ankra onboard` and exits with its
# status. exec, so Ctrl-C reaches ankra alone and nothing runs after it.
run_onboarding() {
	binary=$1
	if ! "$binary" onboard --help >/dev/null 2>&1; then
		if [ "$homebrew_managed" = 1 ]; then
			say "This Homebrew copy of ankra has no onboard command yet. Update it, then run onboarding:"
			say "  brew upgrade ankra && ankra onboard"
			exit 1
		fi
		say "This version of ankra has no onboard command. Sign in with:"
		say "  ankra login"
		exit 0
	fi
	say ""
	say "Starting onboarding: ankra onboard"
	say ""
	cleanup
	trap - EXIT INT TERM
	# Piped into sh, stdin is the script itself; the person's terminal is
	# /dev/tty, when there is one.
	if (true </dev/tty) 2>/dev/null; then
		exec "$binary" onboard </dev/tty
	fi
	exec "$binary" onboard </dev/null
}

main() {
	version=${ANKRA_VERSION:-}
	install_dir=${ANKRA_INSTALL_DIR:-}
	no_onboard=0
	if is_true "${ANKRA_NO_ONBOARD:-}"; then
		no_onboard=1
	fi
	no_modify_path=0
	if is_true "${ANKRA_NO_MODIFY_PATH:-}"; then
		no_modify_path=1
	fi

	while [ "$#" -gt 0 ]; do
		case "$1" in
		--version | -v)
			[ "$#" -ge 2 ] || fail "$1 needs a release, for example: $1 v0.75.0"
			version=$2
			shift 2
			;;
		--version=*)
			version=${1#--version=}
			shift
			;;
		--install-dir)
			[ "$#" -ge 2 ] || fail "--install-dir needs a directory"
			install_dir=$2
			shift 2
			;;
		--install-dir=*)
			install_dir=${1#--install-dir=}
			shift
			;;
		--no-onboard)
			no_onboard=1
			shift
			;;
		--no-modify-path)
			no_modify_path=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			usage >&2
			fail "unknown option: $1"
			;;
		esac
	done

	case "$version" in
	"" | latest) version="" ;;
	*[!A-Za-z0-9._-]*) fail "not a release name: $version" ;;
	v*) ;;
	[0-9]*) version="v$version" ;;
	*) fail "not a release name: $version (expected something like v0.75.0)" ;;
	esac

	if [ -n "${ANKRA_DOWNLOAD_BASE:-}" ]; then
		base=${ANKRA_DOWNLOAD_BASE%/}
	elif [ -n "$version" ]; then
		base="$ANKRA_RELEASES_URL/download/$version"
	else
		base="$ANKRA_RELEASES_URL/latest/download"
	fi

	detect_os
	detect_arch

	trap cleanup EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM

	existing=$(command -v ankra 2>/dev/null || true)
	case "$existing" in
	/*) [ -f "$existing" ] || existing="" ;;
	*) existing="" ;;
	esac

	homebrew_managed=0
	if [ -z "$install_dir" ] && [ -n "$existing" ] && is_homebrew_managed "$existing"; then
		homebrew_managed=1
	fi

	if [ "$homebrew_managed" = 1 ]; then
		say "ankra is managed by Homebrew; update it with: brew upgrade ankra"
		target=$existing
	else
		explicit_install_dir=0
		if [ -n "$install_dir" ]; then
			explicit_install_dir=1
		elif [ -n "$existing" ] && directory_is_writable "$(dirname "$(resolve_path "$existing")")"; then
			install_dir=$(dirname "$(resolve_path "$existing")")
		elif directory_is_writable "${ANKRA_INSTALL_SYSTEM_DIR:-/usr/local/bin}"; then
			install_dir=${ANKRA_INSTALL_SYSTEM_DIR:-/usr/local/bin}
		else
			[ -n "${HOME:-}" ] || fail "HOME is not set, so there is no ~/.ankra/bin to install into; choose a directory with --install-dir"
			install_dir="$HOME/.ankra/bin"
		fi
		case "$install_dir" in
		/*) ;;
		*) install_dir="$(pwd)/$install_dir" ;;
		esac
		target="$install_dir/ankra"

		if [ -e "$target" ] && is_homebrew_managed "$target"; then
			fail "$target is managed by Homebrew; update it with: brew upgrade ankra, or choose another --install-dir"
		fi
		if ! mkdir -p "$install_dir" 2>/dev/null || ! directory_is_writable "$install_dir"; then
			if [ "$explicit_install_dir" = 1 ]; then
				fail "cannot write to $install_dir; choose another --install-dir, or leave it out to install into ~/.ankra/bin (this installer never uses sudo)"
			fi
			fail "cannot write to $install_dir"
		fi

		asset="ankra-cli-${os}-${arch}"
		workdir=$(mktemp -d "${TMPDIR:-/tmp}/ankra-install.XXXXXX") || fail "cannot create a temporary directory"

		say "Downloading $asset${version:+ $version} from $base"
		download "$base/$asset" "$workdir/$asset" progress ||
			fail "could not download $base/$asset${version:+ (does release $version exist?)}"
		[ -s "$workdir/$asset" ] || fail "the download of $base/$asset is empty"

		download "$base/$asset.sha256" "$workdir/$asset.sha256" ||
			fail "the release has no checksum at $base/$asset.sha256, so the download cannot be verified; refusing to install"
		verify_checksum "$workdir/$asset" "$workdir/$asset.sha256" "$base/$asset.sha256"
		say "Checksum verified."

		# Copy under a temporary name in the same directory, then rename: the
		# rename is atomic, so a running ankra or a crash never sees half a
		# binary.
		partial_target="$install_dir/.ankra.install.$$"
		cp "$workdir/$asset" "$partial_target" || fail "cannot write to $install_dir"
		chmod 0755 "$partial_target"
		mv -f "$partial_target" "$target" || fail "cannot replace $target"
		partial_target=""

		if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
			xattr -d com.apple.quarantine "$target" 2>/dev/null || true
		fi
	fi

	installed_version=$("$target" --version 2>&1) ||
		fail "the installed ankra at $target does not run: $installed_version"

	path_files_changed=""
	local_bin_linked=0
	path_export=""
	if [ "$homebrew_managed" = 0 ]; then
		if [ "$install_dir" = "${HOME:-}/.ankra/bin" ]; then
			link_local_bin "$target"
		fi
		if [ "$no_modify_path" = 0 ] && { [ "$install_dir" = "${HOME:-}/.ankra/bin" ] || ! on_path "$install_dir"; }; then
			add_to_path "$install_dir"
		fi
		if ! on_path "$install_dir"; then
			path_export="export PATH=\"$(home_relative "$install_dir"):\$PATH\""
		fi
	fi

	say ""
	if [ "$homebrew_managed" = 1 ]; then
		say "Using the Homebrew ankra at $target ($installed_version)"
	else
		say "Installed $installed_version to $target"
		if [ -n "$path_files_changed" ]; then
			say "Added $(display_path "$install_dir") to PATH in $path_files_changed"
		fi
		if [ "$local_bin_linked" = 1 ]; then
			say "Linked ~/.local/bin/ankra to it"
		fi
		note_other_copies "$target"
		if [ -n "$path_export" ]; then
			say ""
			say "To use ankra in this terminal now, run:"
			say "  $path_export"
			case "$(basename "${SHELL:-sh}")" in
			fish) say "In fish, run instead: fish_add_path $install_dir" ;;
			esac
		fi
	fi

	if [ "$no_onboard" = 1 ]; then
		say ""
		say "Next, sign up or sign in and set up with:"
		say "  ankra onboard"
		exit 0
	fi
	run_onboarding "$target"
}

main "$@"
