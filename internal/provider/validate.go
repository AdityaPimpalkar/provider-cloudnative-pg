package provider

import (
	"encoding/json"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg"
	barman "github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

var _ controller.ReferenceValidator = (*Provider)(nil)

var (
	componentsPath = field.NewPath("spec", "components")
	enginePath     = componentsPath.Key(common.ComponentEngine)
	engineParams   = enginePath.Child("parameters")
)

// Validate checks the rules about the Instance alone. The runtime runs it at
// admission and before every Sync, once the Instance matches the schemas, the
// supported fields and the catalog the Provider declares. On an update at
// admission, in.Old is the stored Instance, so changes it forbids are denied
// when written.
func (p *Provider) Validate(in controller.ValidationInput) controller.ValidationResult {
	instance := in.Instance
	engine, ok := instance.Spec.Components[common.ComponentEngine]
	if !ok {
		return controller.ValidationResult{Errors: field.ErrorList{field.Required(enginePath, "")}}
	}

	var errs field.ErrorList
	errs = append(errs, invalid(enginePath, cnpg.ValidateEngine(engine))...)
	errs = append(errs, invalid(field.NewPath("spec", "backup", "storages"), barman.ValidateSchedules(instance.Spec.Backup))...)
	errs = append(errs, invalid(field.NewPath("spec", "dataSource"), barman.ValidateDataSource(instance.Spec.DataSource))...)
	errs = append(errs, invalid(componentsPath.Key(common.ComponentProxy), validateProxy(instance))...)

	custom, err := engineCustomSpec(engine)
	if err != nil {
		return controller.ValidationResult{Errors: append(errs, field.Invalid(engineParams, field.OmitValueType{}, err.Error()))}
	}
	errs = append(errs, invalid(engineParams, cnpg.ValidateCustomSpec(&custom))...)
	errs = append(errs, invalid(engineParams.Child("postgresql", "synchronous"), cnpg.ValidateSynchronous(engine, &custom))...)
	errs = append(errs, invalid(engineParams.Child("replicationSlots"), cnpg.ValidateReplicationSlots(&custom, engine.Version, instance.Spec.Version))...)
	errs = append(errs, invalid(engineParams.Child("bootstrap", "volumeSnapshot"), cnpg.ValidateVolumeSnapshotBootstrap(&custom, instance.Spec.DataSource != nil))...)
	errs = append(errs, invalid(enginePath.Child("schedulingPolicy"), cnpg.ValidateScheduling(engine.SchedulingPolicy, &custom))...)
	errs = append(errs, invalid(engineParams.Child("extensions"), cnpg.ValidateExtensions(&custom, engine.Version, instance.Spec.Version))...)
	errs = append(errs, invalid(engineParams.Child("extensions", "pgStatStatements"), cnpg.ValidatePgStatStatements(&custom))...)
	errs = append(errs, invalid(field.NewPath("spec", "topology", "parameters", "monitoring", "prometheus"), validatePrometheus(instance, &custom))...)

	var warnings []string
	if in.Old != nil {
		old, err := engineCustomSpec(in.Old.Spec.Components[common.ComponentEngine])
		if err == nil {
			errs = append(errs, cnpg.ValidateExtensionChanges(&old, &custom, engineParams.Child("extensions"))...)
			warnings = append(warnings, cnpg.ExtensionRestartWarnings(&old, &custom, engineParams.Child("extensions"))...)
			if old.PriorityClassName != custom.PriorityClassName {
				warnings = append(warnings, engineParams.Child("priorityClassName").String()+": changing it restarts the instances one at a time")
			}
		}
	}

	return controller.ValidationResult{Errors: errs, Warnings: warnings}
}

// ValidateReferences checks the rules that read the cluster: the extension
// images in the ClusterImageCatalog, and the live Cluster and Database for
// writes admission didn't see, including a change of the HA replication slot
// prefix CloudNativePG would refuse, and the bootstrap VolumeSnapshot: authorized
// for this Instance, and unchanged once the Cluster exists. At admission its errors are warnings; before
// Sync they block it.
func (p *Provider) ValidateReferences(c *controller.Context) (field.ErrorList, error) {
	engine := c.Instance().Spec.Components[common.ComponentEngine]
	custom, err := engineCustomSpec(engine)
	if err != nil {
		// Validate reports it.
		return nil, nil
	}
	extensionsPath := engineParams.Child("extensions")
	slotPrefixPath := engineParams.Child("replicationSlots", "highAvailability", "slotPrefix")
	volumeSnapshotPath := engineParams.Child("bootstrap", "volumeSnapshot")

	var errs field.ErrorList
	for _, check := range []struct {
		path *field.Path
		run  func() error
	}{
		{extensionsPath, func() error {
			_, err := cnpg.ResolveExtensionImages(c, p.extensionCatalog, &custom, engine.Version, c.Instance().Spec.Version)
			return err
		}},
		{extensionsPath, func() error { return cnpg.ValidateExtensionsNotDisabled(c, &custom) }},
		{extensionsPath, func() error { return cnpg.ValidateExtensionsAtCreation(c, &custom) }},
		{slotPrefixPath, func() error { return cnpg.ValidateReplicationSlotsChange(c, &custom) }},
		{volumeSnapshotPath, func() error { return cnpg.ValidateVolumeSnapshot(c, &custom) }},
	} {
		err := check.run()
		if err == nil {
			continue
		}
		// A read that failed for another reason than a missing object is retried.
		if status := apierrors.APIStatus(nil); errors.As(err, &status) && !apierrors.IsNotFound(err) {
			return nil, err
		}
		errs = append(errs, field.Invalid(check.path, field.OmitValueType{}, err.Error()))
	}
	return errs, nil
}

// engineCustomSpec decodes the engine component's parameters.
func engineCustomSpec(engine corev1alpha1.ComponentSpec) (components.CNPGCustomSpec, error) {
	var custom components.CNPGCustomSpec
	if engine.Parameters == nil || len(engine.Parameters.Raw) == 0 {
		return custom, nil
	}
	if err := json.Unmarshal(engine.Parameters.Raw, &custom); err != nil {
		return custom, fmt.Errorf("decode the engine parameters: %w", err)
	}
	return custom, nil
}

// invalid reports err, if any, as an invalid value of the field at path.
func invalid(path *field.Path, err error) field.ErrorList {
	if err == nil {
		return nil
	}
	return field.ErrorList{field.Invalid(path, field.OmitValueType{}, err.Error())}
}
