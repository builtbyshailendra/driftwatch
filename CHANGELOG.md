# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

No release has been tagged yet. The first one will be 0.1.0.

### Added

- `driftwatch check` runs `terraform plan -detailed-exitcode -out` and
  `terraform show -json`, converts an existing saved plan (`--plan-file`), or
  reads plan JSON from a file or stdin (`--plan-json`).
- Classification of managed-resource changes into delete, replace, update and
  create, with severities, and of `resource_drift` entries as changes made
  outside Terraform.
- Attribute-level diffs with sensitive values masked using `before_sensitive`
  and `after_sensitive`, unknown values flagged from `after_unknown`, and
  attributes that force replacement marked from `replace_paths`. Values that
  are only partly sensitive keep their visible parts, and markers that do not
  fit their value mask the whole value.
- `.driftignore` rules for whole resources or single attributes, with glob
  patterns. Map keys match in dotted (`tags.Name`) or bracket
  (`tags["Name"]`) form, and rules that match nothing are reported.
- Incomplete plans (`complete: false`, `deferred_changes`) are flagged in
  every output and the deferred changes are listed.
- `summary.total` in the JSON report counts distinct resources.
- `driftwatch version` reports the module version or commit recorded by the
  Go toolchain when no version is set with `-ldflags`.
- Markdown and JSON (schema version 1) reports.
- GitHub issue sync over the REST API: one issue per workspace, found by a
  hidden marker, then created, updated, or closed when the drift is gone.
- Slack incoming-webhook summaries with a link to the tracking issue.
- Composite GitHub Action under `action/` for scheduled drift checks.
- Plan JSON fixtures generated with Terraform 1.17.1 from real workspaces:
  one using the null, random and local providers, one using only
  `terraform_data` with partly sensitive values.

[Unreleased]: https://github.com/builtbyshailendra/driftwatch/commits/main
