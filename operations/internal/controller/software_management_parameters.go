package controller

import (
	"slices"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// A read request must not persist an unsubmitted draft as the effective
// configuration. read-config may instead return validated current values.
func confirmedManagementParameters(p storedSoftwareOperationPlan, before softwareManagementState, evidence *serviceinstall.ManagementEvidence) map[string]string {
	if !p.Operation.ReadOnly {
		return p.Management.Parameters
	}
	fields := p.Management.Definition.Management.Parameters
	values := map[string]string{}
	for _, field := range fields {
		if field.Type == "CREDENTIAL" {
			continue
		}
		value, exists := before.Parameters[field.Key]
		if !exists {
			value, exists = p.Config.Parameters[field.Key]
		}
		if !exists {
			value = field.Default
		}
		values[field.Key] = value
	}
	if p.Operation.Key != "read-config" || evidence == nil || evidence.Table == nil {
		return values
	}
	if !slices.Contains(evidence.Table.Columns, "key") || !slices.Contains(evidence.Table.Columns, "value") {
		return values
	}
	actual := map[string]string{}
	for _, row := range evidence.Table.Rows {
		if _, allowed := values[row["key"]]; !allowed {
			continue
		}
		if _, duplicate := actual[row["key"]]; duplicate {
			return values
		}
		if _, present := row["value"]; !present {
			return values
		}
		actual[row["key"]] = row["value"]
	}
	if len(actual) != len(values) {
		return values
	}
	if resolved, err := serviceinstall.ResolveParameters(fields, actual, p.Management.ParameterCredentials); err == nil {
		return resolved
	}
	return values
}
