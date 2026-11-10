package controller

import (
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func managedDataDirectory(resources []serviceinstall.ResolvedResource) (directory, identity string) {
	for _, resource := range resources {
		if resource.Key == "active-data" && resource.Type == "PATH_WRITE" && !resource.Release {
			directory, identity = resource.Value, resource.IdentityHash
		}
	}
	return
}

// Management records hold the last confirmed resources. Present their active
// Web listener while keeping the signed installation and job snapshots intact.
func (s *Store) softwareWebView(item softwareInstance) (softwareInstance, error) {
	item.ConfigurationAvailable = s.softwareConfigurationAvailable(item)
	if item.Installation == nil || item.ManagementMode != "MANAGED" {
		return item, nil
	}
	state, err := s.readSoftwareManagement(item.ID)
	if err != nil {
		return item, err
	}
	installed := *item.Installation
	installed.Config = serviceinstall.ProjectWebConfig(installed.Definition, installed.Config, state.Resources)
	if installed.Definition.Web != nil && installed.Definition.Web.URLParameter != "" {
		key := installed.Definition.Web.URLParameter
		if value, confirmed := state.Parameters[key]; confirmed {
			parameters := make(map[string]string, len(installed.Config.Parameters)+1)
			for name, original := range installed.Config.Parameters {
				parameters[name] = original
			}
			parameters[key] = value
			installed.Config.Parameters = parameters
		}
	}
	item.Installation = &installed
	if item.InstallationPaths != nil {
		paths := *item.InstallationPaths
		if directory, identity := managedDataDirectory(state.Resources); directory != "" && deploy.Hex.MatchString(identity) {
			paths.Data = directory
		}
		item.InstallationPaths = &paths
	}
	return item, nil
}

func (s *Store) softwareConfigurationAvailable(item softwareInstance) bool {
	if !managementInstance(item) || item.RuntimeKind == "TOOL" || item.SoftwareType == "DOCKER" {
		return false
	}
	if template, err := s.managementTemplate(item, "", 0); err == nil {
		read, readErr := template.Published.ManagementOperation("read-config")
		write, writeErr := template.Published.ManagementOperation("configure")
		if readErr == nil && writeErr == nil && read.ReadOnly && !write.ReadOnly && read.SupportsRuntime(item.Installation.Config.Mode()) && write.SupportsRuntime(item.Installation.Config.Mode()) {
			return true
		}
	}
	definition := item.Installation.Definition
	return !serviceinstall.IsOfficial(definition) && (definition.Commands == nil || serviceinstall.IsBuiltinCommand(definition)) && baseSoftwareConfigurationAllowed(s.DB, item.ID) == nil
}
