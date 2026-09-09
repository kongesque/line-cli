#!/bin/sh
set -eu

repository=kongesque/line-cli
download_base=${LINE_CLI_DOWNLOAD_BASE:-"https://github.com/$repository/releases/latest/download"}
install_dir=${LINE_CLI_INSTALL_DIR:-"$HOME/.local/bin"}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *)
    printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)
    printf 'Unsupported CPU architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac

archive="line-$os-$arch.tar.gz"
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

download() {
  url=$1
  output=$2
  if ! command -v curl >/dev/null 2>&1; then
    printf '%s\n' 'Install curl and run this installer again.' >&2
    exit 1
  fi
  curl --proto '=https' --tlsv1.2 -fsSL "$url" -o "$output"
}

printf 'Downloading LINE CLI for %s/%s...\n' "$os" "$arch"
download "$download_base/$archive" "$temporary_dir/$archive"
download "$download_base/SHA256SUMS.txt" "$temporary_dir/SHA256SUMS.txt"

expected=$(awk -v archive="$archive" '$2 == archive { print $1 }' "$temporary_dir/SHA256SUMS.txt")
if [ -z "$expected" ] || [ "$(printf '%s\n' "$expected" | wc -l | tr -d ' ')" -ne 1 ]; then
  printf 'Could not find one checksum for %s.\n' "$archive" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$temporary_dir/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$temporary_dir/$archive" | awk '{ print $1 }')
else
  printf '%s\n' 'A SHA-256 checksum tool is required.' >&2
  exit 1
fi

if [ "$actual" != "$expected" ]; then
  printf '%s\n' 'Archive checksum verification failed.' >&2
  exit 1
fi

mkdir -p "$temporary_dir/extracted"
tar -xzf "$temporary_dir/$archive" -C "$temporary_dir/extracted"
if [ ! -f "$temporary_dir/extracted/line" ]; then
  printf '%s\n' 'The release archive does not contain the line executable.' >&2
  exit 1
fi

mkdir -p "$install_dir"
temporary_binary="$install_dir/.line-install.$$"
trap 'rm -rf "$temporary_dir"; rm -f "$temporary_binary"' EXIT HUP INT TERM
cp "$temporary_dir/extracted/line" "$temporary_binary"
chmod 755 "$temporary_binary"
mv -f "$temporary_binary" "$install_dir/line"

printf 'Installed LINE CLI: %s\n' "$install_dir/line"
case ":$PATH:" in
  *":$install_dir:"*) printf '%s\n' 'Ready: line help' ;;
  *)
    path_line='export PATH="$HOME/.local/bin:$PATH"'
    profile=
    if [ -z "${LINE_CLI_INSTALL_DIR+x}" ]; then
      case "${SHELL:-}" in
        */zsh|zsh) profile="$HOME/.zshrc" ;;
        */bash|bash)
          if [ "$os" = darwin ]; then profile="$HOME/.bash_profile"; else profile="$HOME/.bashrc"; fi
          ;;
        */sh|sh|*/dash|dash|*/ksh|ksh) profile="$HOME/.profile" ;;
      esac
    fi
    if [ -n "$profile" ] && { [ ! -e "$profile" ] || [ -w "$profile" ]; }; then
      if ! grep -F "$path_line" "$profile" >/dev/null 2>&1; then
        printf '\n%s\n%s\n' '# Added by the LINE CLI installer' "$path_line" >> "$profile"
      fi
      printf 'Updated PATH in %s. Reopen your terminal, then run: line help\n' "$profile"
    else
      printf '\n%s\n' 'Add LINE CLI to your PATH, then reopen your terminal:'
      printf '  export PATH="%s:$PATH"\n' "$install_dir"
    fi
    ;;
esac

if [ "$os" = linux ]; then
  printf '\n%s\n' 'Before login, make sure a Secret Service keyring is installed, running, and unlocked.'
fi
