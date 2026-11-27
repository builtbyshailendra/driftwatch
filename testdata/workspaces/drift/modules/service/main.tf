terraform {
  required_providers {
    null = {
      source = "hashicorp/null"
    }
    random = {
      source = "hashicorp/random"
    }
  }
}

variable "name" {
  description = "Service name."
  type        = string
}

variable "release" {
  description = "Release deployed for this service."
  type        = string
}

resource "random_integer" "port" {
  min = 8000
  max = 8999

  keepers = {
    name = var.name
  }
}

resource "null_resource" "rollout" {
  triggers = {
    name    = var.name
    release = var.release
    port    = tostring(random_integer.port.result)
  }
}

output "port" {
  description = "Port assigned to the service."
  value       = random_integer.port.result
}
