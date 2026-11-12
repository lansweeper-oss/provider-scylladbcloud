// SPDX-FileCopyrightText: 2024 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane/upjet/v2/pkg/controller"

	rule "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/allowlist/rule"
	connection "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/cluster/connection"
	providerconfig "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/providerconfig"
	cluster "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/scylladbcloud/cluster"
	stack "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/scylladbcloud/stack"
	clusterserverless "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/serverless/cluster"
	peering "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/cluster/vpc/peering"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		rule.Setup,
		connection.Setup,
		providerconfig.Setup,
		cluster.Setup,
		stack.Setup,
		clusterserverless.Setup,
		peering.Setup,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupGated creates all controllers with the supplied logger and adds them to
// the supplied manager gated.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		rule.SetupGated,
		connection.SetupGated,
		providerconfig.SetupGated,
		cluster.SetupGated,
		stack.SetupGated,
		clusterserverless.SetupGated,
		peering.SetupGated,
	} {
		if err := setup(mgr, o); err != nil {
			return err
		}
	}
	return nil
}

// SetupWebhookWithManager registers conversion webhooks for all resource kinds in the group.
func SetupWebhookWithManager(mgr ctrl.Manager) error {
	for _, setup := range []func(ctrl.Manager) error{
		rule.SetupWebhookWithManager,
		connection.SetupWebhookWithManager,
		providerconfig.SetupWebhookWithManager,
		cluster.SetupWebhookWithManager,
		stack.SetupWebhookWithManager,
		clusterserverless.SetupWebhookWithManager,
		peering.SetupWebhookWithManager,
	} {
		if err := setup(mgr); err != nil {
			return err
		}
	}
	return nil
}
