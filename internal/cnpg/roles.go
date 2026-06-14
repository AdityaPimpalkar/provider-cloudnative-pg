package cnpg

import (
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

func ManagedRolesStatus(pg *cnpgv1.Cluster) (controller.Status, bool) {
	if pg.Spec.Managed == nil || len(pg.Spec.Managed.Roles) == 0 {
		return controller.Status{}, false
	}

	roleStatus := pg.Status.ManagedRolesStatus

	var failureMessages []string
	for role, errors := range roleStatus.CannotReconcile {
		failureMessages = append(failureMessages, fmt.Sprintf(
			"managed PostgreSQL role %q cannot be reconciled: %s",
			role,
			strings.Join(errors, "; "),
		))
	}
	if len(failureMessages) > 0 {
		return controller.Failed(strings.Join(failureMessages, "\n")), true
	}

	reconciledRoles := roleStatus.ByStatus[cnpgv1.RoleStatusReconciled]

	var rolesAwaitingReconciliation []string
	for _, roleConfig := range pg.Spec.Managed.Roles {
		if slices.Contains(reconciledRoles, roleConfig.Name) {
			continue
		}
		rolesAwaitingReconciliation = append(rolesAwaitingReconciliation, roleConfig.Name)
	}

	if len(rolesAwaitingReconciliation) == 0 {
		return controller.Status{}, false
	}

	return controller.Provisioning(fmt.Sprintf(
		"waiting for managed PostgreSQL roles to be reconciled: %s",
		strings.Join(rolesAwaitingReconciliation, ", "),
	)), true
}
