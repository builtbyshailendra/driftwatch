variable "password" {
  description = "Fixture-only secret. The tests assert it never appears in a report."
  type        = string
  sensitive   = true
  default     = "fixture-only-Qv7rTz29mWk"
}

variable "unknown_mode" {
  description = "\"object\" stores the credentials object; \"unknown\" makes the new value unknown until apply."
  type        = string
  default     = "object"

  validation {
    condition     = contains(["object", "unknown"], var.unknown_mode)
    error_message = "unknown_mode must be \"object\" or \"unknown\"."
  }
}

variable "retype_mode" {
  description = "\"object\" stores the credentials object; \"text\" replaces it with a string."
  type        = string
  default     = "object"

  validation {
    condition     = contains(["object", "text"], var.retype_mode)
    error_message = "retype_mode must be \"object\" or \"text\"."
  }
}
