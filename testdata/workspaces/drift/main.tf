resource "random_pet" "environment" {
  length = 2

  keepers = {
    release = var.release
  }
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "random_password" "database" {
  length  = 24
  special = false

  keepers = {
    rotation = var.password_rotation
  }
}

resource "terraform_data" "metadata" {
  input = {
    owner       = var.owner
    cost_center = "cc-4410"
    tier        = "gold"
  }
}

resource "null_resource" "deploy" {
  triggers = {
    release     = var.release
    environment = random_pet.environment.id
  }
}

resource "null_resource" "cache_warmer" {
  count = var.enable_cache ? 1 : 0

  triggers = {
    suffix = random_id.suffix.hex
  }
}

resource "local_file" "inventory" {
  filename        = "${path.module}/out/inventory.json"
  file_permission = "0644"
  content = jsonencode({
    environment = random_pet.environment.id
    release     = var.release
    services    = sort(tolist(var.services))
  })
}

resource "local_sensitive_file" "database_env" {
  filename        = "${path.module}/out/database.env"
  file_permission = "0600"
  content         = "DATABASE_PASSWORD=${random_password.database.result}\n"
}

module "service" {
  source   = "./modules/service"
  for_each = var.services

  name    = each.key
  release = var.release
}
