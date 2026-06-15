package terraform

import (
	"fmt"
	"os"
	"path/filepath"
)

// PrepareAdoptWorkspace writes an "adopt" Terraform workspace for an existing
// Beget VM discovered by the reverse-sync reader.
//
// It differs from the normal create workspace (PrepareWorkspace):
//   - a config-driven `import {}` block binds `terraform apply` to the live VM
//     instead of creating a new one (requires Terraform >= 1.5; the agent image
//     ships 1.9);
//   - no beget_ssh_key resource and access.ssh_keys = [] — an externally created
//     VM has its own keys and must not be touched;
//   - lifecycle { ignore_changes = all } freezes the resource so the import can
//     never mutate the real VM (config values become cosmetic).
//
// Deletion still runs `terraform destroy`, so an adopted VM can be removed from
// the console (the import block is ignored on destroy).
//
// variables.tf is reused from the embedded templates so the same tfVars map
// (including ssh_public_key, which is declared-but-unused here) initialises both
// create and adopt workspaces.
func PrepareAdoptWorkspace(workspaceDir, importID string) error {
	if err := os.MkdirAll(workspaceDir, 0750); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}

	vars, err := templateFS.ReadFile("templates/variables.tf")
	if err != nil {
		return fmt.Errorf("read variables.tf: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "variables.tf"), vars, 0640); err != nil {
		return fmt.Errorf("write variables.tf: %w", err)
	}

	main := fmt.Sprintf(adoptMainTF, importID)
	if err := os.WriteFile(filepath.Join(workspaceDir, "main.tf"), []byte(main), 0640); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}
	return nil
}

// adoptMainTF is rendered with a single %q argument: the Beget VM import id.
const adoptMainTF = `terraform {
  required_providers {
    beget = {
      source = "tf.beget.com/beget/beget"
    }
  }
  backend "pg" {}
}

provider "beget" {
  token = var.beget_token
}

data "beget_software" "os" {
  slug = var.software_slug
}

# Adopt an existing VM discovered by the beget-reader. The import block makes
# ` + "`terraform apply`" + ` bind to the live resource instead of creating one;
# ignore_changes = all freezes it so the import never mutates the real VM.
import {
  to = beget_compute_instance.app_server
  id = %q
}

resource "beget_compute_instance" "app_server" {
  name   = var.server_name
  region = var.region

  configuration = {
    cpu       = var.cpu
    ram_mb    = var.ram_mb
    disk_mb   = var.disk_mb
    cpu_class = "normal_cpu"
  }

  image = {
    software = {
      id = data.beget_software.os.id
    }
  }

  access = {
    ssh_keys = []
  }

  lifecycle {
    ignore_changes = all
  }
}

output "vm_ip" {
  value = beget_compute_instance.app_server.ip_address
}

output "vm_id" {
  value = beget_compute_instance.app_server.id
}
`
