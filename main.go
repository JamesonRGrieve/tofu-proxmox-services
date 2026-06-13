// SPDX-License-Identifier: AGPL-3.0-or-later

// Command proxmox-services is the OpenTofu/Terraform provider plugin entrypoint
// for orchestrating service installs on Proxmox guests via the neighbouring
// Ansible repo. It drives per-guest installs (proxmoxsvc_service) and exposes
// each service's about.json contract (proxmoxsvc_service_metadata) so token
// dependencies between services can be wired declaratively. Companion to the
// jamesonrgrieve/proxmox provider, which creates the guests.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/JamesonRGrieve/tofu-proxmox-services/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/jamesonrgrieve/proxmox-services",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
