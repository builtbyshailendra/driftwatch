# Terraform drift report: drift-demo

**Drift detected: 3 to delete, 5 to replace, 1 to update, 1 to create, 1 changed outside Terraform.**

Generated 2026-11-07 06:00 UTC with Terraform 1.17.1.

| Change | Count |
|---|---:|
| Delete | 3 |
| Replace | 5 |
| Update in place | 1 |
| Create | 1 |
| Changed outside Terraform | 1 |
| Ignored by rules | 0 |

## Changed outside Terraform

Terraform found these objects changed since the last apply, without going through Terraform.

### `local_file.inventory` delete

Severity **high**, provider `hashicorp/local`.

## Planned changes

Applying the current configuration would make these changes.

### `module.service["worker"].null_resource.rollout` delete

Severity **high**, provider `hashicorp/null`, because its module call was removed from configuration.

### `module.service["worker"].random_integer.port` delete

Severity **high**, provider `hashicorp/random`, because its module call was removed from configuration.

### `null_resource.cache_warmer[0]` delete

Severity **high**, provider `hashicorp/null`, because count no longer includes this index.

### `local_sensitive_file.database_env` replace

Severity **high**, provider `hashicorp/local`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `content` | _(sensitive value)_ | _(sensitive value)_ | forces replacement, sensitive |
| `content_base64sha256` | `"YHpGcbFHZfietomE71Qf7li+/qZouzPqbzwUWTNnDd0="` | _(known after apply)_ |  |
| `content_base64sha512` | `"YPj/GNL0k6Yu+OTlm9oaFcs8EHtnOpM7mhU93E6GcgyWK3iu9OX+dhicXrPLuW251R/Cf9C4yFXV...` | _(known after apply)_ |  |
| `content_md5` | `"bb1b6064693f8b30b11703cc22591187"` | _(known after apply)_ |  |
| `content_sha1` | `"fe672ace37880a495856b72c203c00e2970a6562"` | _(known after apply)_ |  |
| `content_sha256` | `"607a4671b14765f89eb68984ef541fee58befea668bb33ea6f3c145933670ddd"` | _(known after apply)_ |  |
| `content_sha512` | `"60f8ff18d2f493a62ef8e4e59bda1a15cb3c107b673a933b9a153ddc4e86720c962b78aef4e5...` | _(known after apply)_ |  |
| `id` | `"fe672ace37880a495856b72c203c00e2970a6562"` | _(known after apply)_ |  |

### `module.service["api"].null_resource.rollout` replace

Severity **high**, provider `hashicorp/null`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `id` | `"4187770975788110611"` | _(known after apply)_ |  |
| `triggers.release` | `"2026.11.0"` | `"2026.11.1"` | forces replacement |

### `null_resource.deploy` replace

Severity **high**, provider `hashicorp/null`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `id` | `"7911440036760242250"` | _(known after apply)_ |  |
| `triggers.environment` | `"assuring-hog"` | _(known after apply)_ | forces replacement |
| `triggers.release` | `"2026.11.0"` | `"2026.11.1"` | forces replacement |

### `random_password.database` replace

Severity **high**, provider `hashicorp/random`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `bcrypt_hash` | _(sensitive value)_ | _(sensitive value)_ | sensitive |
| `id` | `"none"` | _(known after apply)_ |  |
| `keepers.rotation` | `"2026-q4"` | `"2027-q1"` | forces replacement |
| `result` | _(sensitive value)_ | _(sensitive value)_ | sensitive |

### `random_pet.environment` replace

Severity **high**, provider `hashicorp/random`, because an attribute that forces replacement changed.

| Attribute | Before | After | Note |
|---|---|---|---|
| `id` | `"assuring-hog"` | _(known after apply)_ |  |
| `keepers.release` | `"2026.11.0"` | `"2026.11.1"` | forces replacement |

### `terraform_data.metadata` update

Severity **medium**, provider `terraform.io/builtin/terraform`.

| Attribute | Before | After | Note |
|---|---|---|---|
| `input.owner` | `"platform-team"` | `"data-platform"` |  |
| `output` | `{"cost_center":"cc-4410","owner":"platform-team","tier":"gold"}` | _(known after apply)_ |  |

### `local_file.inventory` create

Severity **low**, provider `hashicorp/local`.
