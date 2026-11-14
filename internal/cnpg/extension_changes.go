package cnpg

import (
	"slices"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

// restarts reports whether turning the extension on or off restarts the
// instances: it mounts an image volume, preloads a library or sets server
// parameters (pgAudit's make the operator preload it).
func (e extension) restarts() bool {
	return e.catalogEntry != "" || len(e.preloadLibraries) > 0 || len(e.defaultParameters) > 0
}

// ValidateExtensionChanges checks an update of the extension toggles against
// the stored ones: an extension created in the database cannot be turned off,
// and a creation-only extension cannot be turned on. path is the extensions
// parameter. ValidateExtensionsNotDisabled and ValidateExtensionsAtCreation
// check the same against the live cluster, for writes admission didn't see.
func ValidateExtensionChanges(old, updated *components.CNPGCustomSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	enabled := enabledExtensions(updated)
	for _, ext := range firstClassExtensions {
		was, is := ext.enabled(old), ext.enabled(updated)
		enabledPath := path.Child(ext.field, "enabled")
		switch {
		case was && !is && ext.sqlExtension != "":
			// Switching TimescaleDB editions keeps the SQL extension; the image
			// change is rejected below as a creation-only enable.
			if slices.ContainsFunc(enabled, func(e extension) bool { return e.sqlExtension == ext.sqlExtension }) {
				continue
			}
			errs = append(errs, field.Forbidden(enabledPath, "cannot be disabled once enabled"))
		case !was && is && ext.creationOnly():
			errs = append(errs, field.Forbidden(enabledPath, "can only be enabled when the instance is created"))
		}
	}
	return errs
}

// ExtensionRestartWarnings returns a warning for each extension an update
// turns on or off that restarts the instances, leaving out the changes
// ValidateExtensionChanges forbids.
func ExtensionRestartWarnings(old, updated *components.CNPGCustomSpec, path *field.Path) []string {
	forbidden := map[string]bool{}
	for _, err := range ValidateExtensionChanges(old, updated, path) {
		forbidden[err.Field] = true
	}
	var warnings []string
	for _, ext := range firstClassExtensions {
		was, is := ext.enabled(old), ext.enabled(updated)
		enabledPath := path.Child(ext.field, "enabled").String()
		if was == is || !ext.restarts() || forbidden[enabledPath] {
			continue
		}
		action := "Enabling"
		if was {
			action = "Disabling"
		}
		warnings = append(warnings, enabledPath+": "+action+" "+ext.field+" restarts the instances")
	}
	return warnings
}
