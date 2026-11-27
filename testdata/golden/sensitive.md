# Terraform drift report: drift-demo

**Drift detected: 2 to replace, 1 to update.**

Generated 2026-11-07 06:00 UTC with Terraform 1.17.1.

| Change | Count |
|---|---:|
| Delete | 0 |
| Replace | 2 |
| Update in place | 1 |
| Create | 0 |
| Changed outside Terraform | 0 |
| Ignored by rules | 0 |

## Planned changes

Applying the current configuration would make these changes.

### `terraform_data.retype` replace

Severity **high**, provider `terraform.io/builtin/terraform`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `id` | `"73940706-6710-adac-9650-eec8a134fa5f"` | _(known after apply)_ |  |
| `triggers_replace` | `{"password":"(sensitive value)","user":"admin"}` | `"admin"` | forces replacement, sensitive |

### `terraform_data.unknown` replace

Severity **high**, provider `terraform.io/builtin/terraform`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `id` | `"8bfcdd7b-e708-a976-90ad-644c8778f546"` | _(known after apply)_ |  |
| `triggers_replace` | `{"password":"(sensitive value)","user":"admin"}` | _(sensitive value)_ | forces replacement, sensitive |

### `terraform_data.echo` update

Severity **medium**, provider `terraform.io/builtin/terraform`.

| Attribute | Before | After | Note |
|---|---|---|---|
| `input` | `{"password":"(sensitive value)","user":"admin"}` | _(sensitive value)_ | sensitive |
| `output` | `{"password":"(sensitive value)","user":"admin"}` | _(known after apply)_ | sensitive |
