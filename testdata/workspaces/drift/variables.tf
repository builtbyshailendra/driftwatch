variable "release" {
  description = "Application release identifier. Changing it replaces the deployment trigger."
  type        = string
  default     = "2026.11.0"
}

variable "owner" {
  description = "Owning team recorded in workspace metadata. Changing it is an in-place update."
  type        = string
  default     = "platform-team"
}

variable "enable_cache" {
  description = "Whether the cache warmer resource exists. Setting false plans a delete."
  type        = bool
  default     = true
}

variable "password_rotation" {
  description = "Rotation epoch for the database password. Changing it replaces the password."
  type        = string
  default     = "2026-q4"
}

variable "services" {
  description = "Services deployed through the service module."
  type        = set(string)
  default     = ["api", "worker"]
}
