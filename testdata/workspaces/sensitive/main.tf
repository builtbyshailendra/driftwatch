# Fixture workspace for sensitive values nested inside larger values. It uses
# only the built-in terraform_data resource, so "terraform init" needs no
# provider downloads.
#
# Both resources first store an object whose "password" key is sensitive.
# Changing var.unknown_mode or var.retype_mode then makes Terraform plan a
# change where only part of the old value is marked sensitive:
#   - unknown_mode = "unknown" turns the whole new value unknown until apply,
#   - retype_mode = "text" replaces the object with a plain string.
#
# terraform_data.echo stores the same object in "input". terraform_data copies
# input to its "output" attribute without the sensitivity marks, so the old
# output holds the password in plain text with nothing marking it.

locals {
  credentials = {
    user     = "admin"
    password = var.password
  }

  unknown_inputs = {
    object  = local.credentials
    unknown = jsondecode(jsonencode(merge(nonsensitive(local.credentials), { issued_at = timestamp() })))
  }

  retype_inputs = {
    object = local.credentials
    text   = "admin"
  }
}

resource "terraform_data" "unknown" {
  triggers_replace = local.unknown_inputs[var.unknown_mode]
}

resource "terraform_data" "retype" {
  triggers_replace = local.retype_inputs[var.retype_mode]
}

resource "terraform_data" "echo" {
  input = local.unknown_inputs[var.unknown_mode]
}
