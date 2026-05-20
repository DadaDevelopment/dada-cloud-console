variable "beget_token" {
  type      = string
  sensitive = true
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

variable "software_id" {
  type        = number
  description = "Beget software ID for Ubuntu 22.04 (query once via data source)"
}

variable "ssh_key_id" {
  type        = string
  description = "ID of SSH key pre-registered in Beget account"
}
