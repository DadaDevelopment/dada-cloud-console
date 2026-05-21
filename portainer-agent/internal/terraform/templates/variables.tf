variable "beget_token" {
  type        = string
  sensitive   = true
  description = "Beget API token (generate at: Beget panel → Cloud → API tokens)"
}

variable "server_name" {
  type = string
}

variable "region" {
  type    = string
  default = "ru1"
}

variable "cpu" {
  type    = number
  default = 2
}

variable "ram_mb" {
  type    = number
  default = 2048
}

variable "disk_mb" {
  type    = number
  default = 20480
}

variable "software_slug" {
  type        = string
  description = "Beget software slug, e.g. ubuntu-24-04"
  default     = "ubuntu-24-04"
}

variable "ssh_public_key" {
  type        = string
  description = "OpenSSH public key (ssh-rsa ...) to register and inject into the VM"
}
