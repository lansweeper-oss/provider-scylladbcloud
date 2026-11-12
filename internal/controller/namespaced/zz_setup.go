// SPDX-FileCopyrightText: 2026 The Crossplane Authors <https://crossplane.io>
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/crossplane/upjet/v2/pkg/controller"

	providerconfig "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/providerconfig"
	allowlistrule "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/scylladbcloud/allowlistrule"
	cluster "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/scylladbcloud/cluster"
	clusterconnection "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/scylladbcloud/clusterconnection"
	serverlesscluster "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/scylladbcloud/serverlesscluster"
	vpcpeering "github.com/lansweeper-oss/provider-scylladbcloud/internal/controller/namespaced/scylladbcloud/vpcpeering"
)

// Setup creates all controllers with the supplied logger and adds them to
// the supplied manager.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	for _, setup := range []func(ctrl.Manager, controller.Options) error{
		providerconfig.Setup,
		allowlistrule.Setup,
		cluster.Setup,
		clusterconnection.Setup,
		serverlesscluster.Setup,
		vpcpeering.Setup,
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
		providerconfig.SetupGated,
		allowlistrule.SetupGated,
		cluster.SetupGated,
		clusterconnection.SetupGated,
		serverlesscluster.SetupGated,
		vpcpeering.SetupGated,
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
		providerconfig.SetupWebhookWithManager,
		allowlistrule.SetupWebhookWithManager,
		cluster.SetupWebhookWithManager,
		clusterconnection.SetupWebhookWithManager,
		serverlesscluster.SetupWebhookWithManager,
		vpcpeering.SetupWebhookWithManager,
	} {
		if err := setup(mgr); err != nil {
			return err
		}
	}
	return nil
}
