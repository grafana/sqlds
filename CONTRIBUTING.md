# Contributing to sqlds

## Pull request titles

The repository squash-merges every PR and uses the PR title as the commit subject. The title must follow the [Conventional Commits](https://www.conventionalcommits.org/) format, and a required check enforces this:

```text
type(scope): subject
```

The scope is optional. The subject must start with a lowercase letter. The type decides the changelog section and the version bump of the next release:

| Type       | Changelog section           | Version effect |
| ---------- | --------------------------- | -------------- |
| `feat`     | 🎉 Features                 | minor          |
| `fix`      | 🐛 Bug Fixes                | patch          |
| `perf`     | ⚡ Performance Improvements | patch          |
| `refactor` | ♻️ Code Refactoring         | patch          |
| `docs`     | 📝 Documentation            | patch          |
| `test`     | ✅ Tests                    | patch          |
| `build`    | 🏗️ Builds                   | patch          |
| `ci`       | 🤖 Continuous Integration   | patch          |
| `revert`   | ⏪ Reverts                  | patch          |
| `chore`    | hidden                      | none           |

Examples:

```text
feat(connector): apply Grafana SQL pool defaults to cached connections
fix: defer bootstrap Connect so CallResource works without a live DB
chore(deps): update backend dependencies
```

### Breaking changes

A `!` before the colon marks a breaking change and bumps the major version:

```text
feat(query)!: remove the deprecated Interpolate function
```

sqlds is a Go module with the major version in its import path (`github.com/grafana/sqlds/v5`). A new major version also needs the module path changed in `go.mod` and in every import, in the same PR. Without that change, Go tooling rejects the new version because the major in the tag does not match the module path.

## Releasing

[release-please](https://github.com/googleapis/release-please) cuts releases. After every push to `main`, it opens or updates a release PR titled `chore(main): release X.Y.Z`. The PR contains the new `CHANGELOG.md` section, generated from the conventional commits since the previous release.

To release:

1. Review the release PR. If an entry needs better wording, edit `CHANGELOG.md` on the release PR branch. Do this last, because release-please rewrites the branch on the next push to `main`.
2. Merge the release PR. release-please creates the `vX.Y.Z` tag and the GitHub release. The Go module proxy serves the new version from the tag.

Routine dependency updates land as `chore(deps)` and do not produce a release on their own. Security updates from vulnerability alerts land as `fix(deps)` and produce a patch release. To cut a release with no releasable commits, add a `Release-As: x.y.z` footer to a commit on `main`.
