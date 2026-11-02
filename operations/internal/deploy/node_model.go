package deploy

import (
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const NodeEnvironmentCapabilityVersion = 1

type NodeEnvironmentRequest struct {
	Context        serviceinstall.NodeContext `json:"context"`
	Action         string                     `json:"action"`
	NodeVersion    string                     `json:"nodeVersion,omitempty"`
	PackageVersion string                     `json:"packageVersion,omitempty"`
	ExpectedHash   string                     `json:"expectedHash,omitempty"`
	Source         serviceinstall.Source      `json:"source"`
	DeleteData     bool                       `json:"deleteData,omitempty"`
}

type NodeVersionObservation struct {
	Version        string              `json:"version"`
	BinaryPath     string              `json:"binaryPath"`
	BinaryHash     string              `json:"binaryHash"`
	NPMVersion     string              `json:"npmVersion"`
	PNPMVersion    string              `json:"pnpmVersion"`
	GlobalPackages []NodeGlobalPackage `json:"globalPackages"`
}
type NodeGlobalPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type NodeEnvironmentObservation struct {
	EnvironmentID  string                   `json:"environmentId"`
	Account        string                   `json:"account"`
	AccountUID     string                   `json:"accountUid"`
	RootPath       string                   `json:"rootPath"`
	ManagerType    string                   `json:"managerType"`
	NVMVersion     string                   `json:"nvmVersion"`
	NVMExecHash    string                   `json:"nvmExecHash,omitempty"`
	NVMHash        string                   `json:"nvmHash"`
	DefaultVersion string                   `json:"defaultVersion"`
	Versions       []NodeVersionObservation `json:"versions"`
	Fingerprint    string                   `json:"fingerprint"`
	ObservedAt     string                   `json:"observedAt"`
	Exists         bool                     `json:"exists"`
}
type NodeEnvironmentPreflight struct {
	RemovedPaths []string                         `json:"removedPaths,omitempty"`
	Ready        bool                             `json:"ready"`
	Context      serviceinstall.NodeContext       `json:"context"`
	Observation  *NodeEnvironmentObservation      `json:"observation,omitempty"`
	Checks       []serviceinstall.Check           `json:"checks"`
	Commands     []serviceinstall.OfficialCommand `json:"commands"`
}

func (n NodeEnvironmentRequest) Validate() error {
	if n.DeleteData && n.Action != "REMOVE_ENVIRONMENT" {
		return errors.New("NODE_DELETE_DATA_UNEXPECTED")
	}
	if n.Context.Validate() != nil {
		return errors.New("NODE_ENVIRONMENT_INVALID")
	}
	switch n.Action {
	case "INITIALIZE", "OBSERVE", "DISCOVER", "REMOVE_ENVIRONMENT":
		if n.NodeVersion != "" || n.PackageVersion != "" {
			return errors.New("NODE_ACTION_INVALID")
		}
	case "UPDATE_NVM":
		if n.NodeVersion != "" {
			return errors.New("NODE_ACTION_INVALID")
		}
		if _, err := nodeEnvironmentDefinition(n); err != nil {
			return err
		}
	case "SET_DEFAULT":
		if !serviceinstall.NodeVersionPattern.MatchString(n.NodeVersion) || n.PackageVersion != "" {
			return errors.New("NODE_VERSION_INVALID")
		}
	case "UPDATE_NPM", "INSTALL_PNPM", "UPDATE_PNPM", "REMOVE_PNPM":
		if n.Action == "REMOVE_PNPM" {
			if n.PackageVersion != "" {
				return errors.New("NODE_PACKAGE_VERSION_UNEXPECTED")
			}
		} else if _, err := nodeEnvironmentDefinition(n); err != nil {
			return err
		}
		if !serviceinstall.NodeVersionPattern.MatchString(n.NodeVersion) {
			return errors.New("NODE_VERSION_INVALID")
		}
	default:
		return errors.New("NODE_ACTION_INVALID")
	}
	if n.ExpectedHash != "" && !Hex.MatchString(n.ExpectedHash) {
		return errors.New("NODE_EXPECTED_HASH_INVALID")
	}
	return nil
}

// Empty historical manager versions remain bound to the original context;
// they never start selecting a newer default after a controller upgrade.
func nodeEnvironmentDefinition(n NodeEnvironmentRequest) (serviceinstall.Definition, error) {
	version := n.PackageVersion
	if n.Action == "INITIALIZE" || n.Action == "UPDATE_NVM" && version == "" {
		version = n.Context.NVMVersion
	}
	return serviceinstall.NodeEnvironmentRecipe(n.Action, version)
}
