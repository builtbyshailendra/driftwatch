# Terraform drift report: drift-demo

**Drift detected: 1 to update (plan incomplete, 2 deferred).**

> **Warning:** this plan is incomplete. Terraform deferred 2 changes to a later run, so the report may not show all drift. Apply and run the check again to see the rest.

Generated 2026-11-07 06:00 UTC with Terraform 1.17.1.

| Change | Count |
|---|---:|
| Delete | 0 |
| Replace | 0 |
| Update in place | 1 |
| Create | 0 |
| Changed outside Terraform | 0 |
| Ignored by rules | 0 |
| Deferred to a later run | 2 |

## Planned changes

Applying the current configuration would make these changes.

### `terraform_data.cluster` update

Severity **medium**, provider `terraform.io/builtin/terraform`.

| Attribute | Before | After | Note |
|---|---|---|---|
| `input.node_count` | `3` | `5` |  |
| `output` | `{"node_count":3}` | _(known after apply)_ |  |

## Deferred changes

Terraform could not plan these yet and will plan them in a later run.

| Resource | Actions | Reason |
|---|---|---|
| `kubernetes_namespace.apps` | update | its provider configuration is known only after apply |
| `terraform_data.node[*]` | create | count or for_each is known only after apply |
