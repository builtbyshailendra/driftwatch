output "environment" {
  description = "Generated environment name."
  value       = random_pet.environment.id
}

output "service_ports" {
  description = "Port assigned to each service."
  value       = { for name, svc in module.service : name => svc.port }
}
