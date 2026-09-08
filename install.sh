#!/bin/sh
set -eu
cd "$(dirname "$0")"

force=false
if [ "${1:-}" = "--force" ]; then force=true; shift; fi
if [ "${1:-}" = "--help" ]; then
  printf '%s\n' 'Usage: ./install.sh [--force] [DIRECTORY]' 'Default: ~/.local/bin. Use --force to replace an existing installation.'
  exit 0
fi
if [ "$#" -gt 1 ]; then printf '%s\n' 'Too many arguments; use ./install.sh --help' >&2; exit 1; fi
destination=${1:-"$HOME/.local/bin"}
case "$destination" in /*) ;; *) destination="$PWD/$destination" ;; esac
filename=line
if [ "$("${GO:-go}" env GOOS)" = windows ]; then filename=line.exe; fi
if { [ -e "$destination/$filename" ] || [ -L "$destination/$filename" ]; } && [ "$force" = false ]; then
  printf 'Already exists: %s\nRun with --force to replace it.\n' "$destination/$filename" >&2
  exit 1
fi
./build.sh
mkdir -p "$destination"
install -m 755 "bin/$filename" "$destination/$filename"
printf 'Installed: %s\n' "$destination/$filename"
case ":$PATH:" in
  *":$destination:"*) printf '%s\n' 'Ready: line help' ;;
  *) printf '%s\n' 'Add the installation directory to PATH.'
     if [ "$destination" = "$HOME/.local/bin" ]; then printf '%s\n' 'For zsh/bash: export PATH="$HOME/.local/bin:$PATH"'; fi ;;
esac
