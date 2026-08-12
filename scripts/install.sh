#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_dir=$(dirname -- "$script_dir")
install_dir=${CHARTA_INSTALL_DIR:-"$HOME/.local/bin"}
target="$install_dir/charta"

find_binary() {
	if [ "$#" -gt 0 ] && [ -n "$1" ]; then
		if [ ! -f "$1" ]; then
			printf 'Binary not found: %s\n' "$1" >&2
			exit 1
		fi
		printf '%s\n' "$1"
		return
	fi

	for candidate in \
		"$project_dir/bin/tui-db" \
		"$project_dir/bin/charta" \
		"$script_dir/tui-db" \
		"$script_dir/charta"
	do
		if [ -f "$candidate" ]; then
			printf '%s\n' "$candidate"
			return
		fi
	done

	printf 'Linux binary not found. Run "make build" first or pass its path to this installer.\n' >&2
	exit 1
}

select_profile() {
	case "${SHELL:-}" in
		*/bash)
			profile="$HOME/.bashrc"
			path_line='export PATH="$HOME/.local/bin:$PATH"'
			;;
		*/zsh)
			profile="$HOME/.zshrc"
			path_line='export PATH="$HOME/.local/bin:$PATH"'
			;;
		*/fish)
			profile="$HOME/.config/fish/config.fish"
			path_line='fish_add_path --prepend "$HOME/.local/bin"'
			;;
		*)
			profile="$HOME/.profile"
			path_line='export PATH="$HOME/.local/bin:$PATH"'
			;;
	esac
}

source_binary=$(find_binary "${1:-}")
mkdir -p "$install_dir"
install -m 755 "$source_binary" "$target"

path_updated=false
case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*)
		if [ "$install_dir" = "$HOME/.local/bin" ]; then
			select_profile
			mkdir -p "$(dirname -- "$profile")"
			if [ ! -f "$profile" ] || ! grep -Fqx "$path_line" "$profile"; then
				{
					printf '\n# charta\n'
					printf '%s\n' "$path_line"
				} >>"$profile"
			fi
			path_updated=true
		else
			printf 'Installed in %s, but CHARTA_INSTALL_DIR must be added to PATH manually.\n' "$install_dir" >&2
		fi
		;;
esac

printf 'charta installed at %s\n' "$target"
if [ "$path_updated" = true ]; then
	printf 'PATH updated in %s. Open a new terminal, then run: charta\n' "$profile"
else
	printf 'Run: charta\n'
fi
