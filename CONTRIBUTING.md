# Contributing

Use the [development guide](docs/DEVELOPMENT.md) for build commands,
test tiers, platform checks, and release procedures.

## Before you submit

- Keep changes focused. Explain the problem, resulting behavior, limitations,
  and any required upgrade action.
- Update the user guide, translations, JSON reference, security record, and
  changelog when the change affects them. See the [documentation map](docs/README.md).
- Run the relevant checks from the development guide. Report which checks you
  ran and any platform or live-service checks that remain outstanding.
- Use synthetic fixtures and fake APIs for ordinary tests. Live LINE tests
  require explicit authorization for the recipient, content, and actions.
- For protocol changes, explain the supporting evidence and preserve source
  provenance and upstream notices.

## Issues and pull requests

Use [GitHub Issues](https://github.com/kongesque/line-cli/issues) for bug reports
and feature requests. For bugs, include `line version`, your OS, the command, and
a redacted error. Omit passwords, tokens, QR login values, private messages,
encrypted chunks, and raw server response bodies from reports and logs.

Link related issues in pull requests. Do not edit generated `pkg/ltsm` files;
see [AGENTS.md](AGENTS.md) for the generated files that must remain untouched.
