# Releasing LINE CLI

This checklist is for maintainers. Users should start with the
[update guide](CLI.md#updating-line-cli). Keep changes under **Unreleased** in
the [changelog](../CHANGELOG.md) until a version and release date are chosen.

## 1. Choose the release scope

- Compare the last release with the candidate commit. Describe user-visible
  additions, changed defaults, restrictions, and upgrade actions in the changelog.
- Use a minor version for new commands/options or behavior changes, and a patch
  version for compatible fixes. Do not promise live interoperability based only
  on synthetic tests or static protocol evidence.
- For this candidate, call out encryption consent, file-specific capabilities,
  disabled encrypted native images, and stopping watchers before upgrading.
- Keep detailed evidence in [SECURITY-CHANGES.md](SECURITY-CHANGES.md); release notes
  should explain the effect on users and link to that record.

## 2. Review documentation

- Check examples and limitations against actual command help and implementation.
  Keep English, Thai, Japanese, and Traditional Chinese READMEs/guides aligned.
  Translate prose, but preserve command flags, JSON fields, and stable anchors.
- Check relative links and heading anchors, including inside the extracted
  release archive. Source/development references may link to GitHub.
- Keep the Go module, compiler guard, build instructions, CI and release pins in
  sync. The reviewed toolchain is currently **Go 1.27.2**, not a minimum-version
  claim that accepts every future compiler.
- Review stdout/stderr, partial results, exit codes, and mutation outcomes in
  [ERRORS.md](ERRORS.md) and [CONTENT.md](CONTENT.md). Existing legacy JSON shapes
  should not be described as new envelopes.
- Run `git diff --check`. Record checks actually run and any remaining gates.

## 3. Validate the candidate

Use a clean checkout and the reviewed Go compiler:

```sh
./scripts/check-toolchain.sh
./build.sh
./bin/line version
./bin/line help
./bin/line send --help
./bin/line messages --help
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go test -count=1 -timeout=20m ./pkg ./pkg/ltsm
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
staticcheck ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
./scripts/security-check.sh ./bin/line
sh -n scripts/install-release.sh
```

`staticcheck` must be installed separately. See [TESTING.md](TESTING.md) for
platform-specific checks, optional dependencies, and test tiers. Do not edit
generated `pkg/ltsm` code or include it in vet/staticcheck.

Require the [checks workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli.yml)
to pass on the exact candidate commit, including the native Linux Secret Service
and Windows DPAPI tests. Cross-compilation does not replace native CI.
Ordinary validation uses synthetic data and fake APIs. Live LINE tests need
explicit authorization for recipient, content, and actions; this checklist
does not authorize messages, uploads, logins, or other live actions.

## 4. Rehearse packaging without publishing

From GitHub Actions, run **LINE CLI release artifacts** on the candidate branch,
or use GitHub CLI after replacing `CANDIDATE_BRANCH`:

```sh
gh workflow run cli-release.yml --ref CANDIDATE_BRANCH
```

Select a branch, not a version tag. The publish job runs on `refs/tags/v*`;
dispatching against a version tag can publish a release. A branch dispatch only
builds and uploads Actions artifacts, with a branch-name development version.

Inspect all six target artifacts: Linux, Windows, and macOS, each amd64/arm64.
Each outer Actions artifact contains `line-OS-ARCH.tar.gz`. Extract that tarball
and check:

- `line` or `line.exe` is at the archive root with the expected target/architecture.
- `SHA256SUMS` verifies the executable. Run `sha256sum -c SHA256SUMS` or
  `shasum -a 256 -c SHA256SUMS` inside the extracted directory.
- `LICENSE`, all `README*.md`, `banner.png`, `CHANGELOG.md`, `TOKEN_SESSION.md`, `AGENTS.md`,
  `docs/`, `readme/`, and `internal/session/HEADLESS.md` are included. Open the
  guide and follow its relative links. Do not recreate a root `CLI.md` alias.
- On matching native hosts, run the extracted binary's `version` and `help`.
  No LINE login is needed. Review source and binary scan results for every target.

Release binaries remain unsigned and unnotarized; checksums verify correspondence
to the release assets and are not a code-signing identity.

## 5. Tag and publish

Before tagging, move **Unreleased** entries to the chosen version/date, update
comparison links, and commit those final notes. Recheck CI on this exact commit.

**Pushing any `v*` tag starts packaging and automatically creates a public GitHub
release after the package matrix passes.** The workflow currently uses generated
notes and does not automatically mark prerelease tags as prereleases. Do not push
a rehearsal or prerelease tag through this path without first adjusting the
publication workflow. Use the branch rehearsal above.

Create an annotated stable-version tag on the validated commit and push only
that tag when ready to publish. See the
[release workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli-release.yml).

## 6. Verify distribution

- Confirm the public tag, version string, six archives, and `SHA256SUMS.txt`
  identify the intended release. This root checksum file covers archives;
  each archive's `SHA256SUMS` covers its executable.
- Replace generated commit listings with curated highlights, upgrade actions,
  security restrictions, and links to the versioned changelog/security record.
- Update `kongesque/tap` with the release URLs and archive checksums; verify
  `brew upgrade line-cli`. Check standalone installers and `line update --check`
  against the published assets on supported hosts.
- Check installation and `version`/`help` without replacing a real user's saved
  LINE session. Restart existing automation only after the upgrade is verified.
- Record native-host checks and outstanding live interoperability limits in the
  release notes. Do not present a release as validated solely because packaging passed.
