package controller

import (
	"database/sql"
	"errors"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/plugin"
)

type pluginPlan struct {
	ID             string            `json:"id"`
	TargetID       string            `json:"targetId"`
	TargetSnapshot string            `json:"targetSnapshot"`
	Capabilities   string            `json:"capabilities"`
	Definition     plugin.Definition `json:"definition"`
	ExpiresAt      string            `json:"expiresAt"`
}

type pluginInstallation struct {
	ID              string `json:"id"`
	TargetID        string `json:"targetId"`
	Key             string `json:"pluginKey"`
	PluginVersion   string `json:"pluginVersion"`
	Kind            string `json:"kind"`
	InstallMode     string `json:"installMode"`
	ImageRef        string `json:"imageRef"`
	ImageDigest     string `json:"imageDigest"`
	ImageID         string `json:"imageId"`
	RecipeRevision  string `json:"recipeRevision"`
	Platform        string `json:"platform"`
	Status          string `json:"status"`
	ObservedVersion string `json:"observedVersion"`
	LastJobID       string `json:"lastJobId"`
	ReferenceCount  int    `json:"referenceCount"`
	ErrorCode       string `json:"errorCode"`
	ObservedAt      string `json:"observedAt"`
	Version         int    `json:"version"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

const pluginColumns = "id,target_id,plugin_key,plugin_version,kind,install_mode,image_ref,image_digest,image_id,recipe_revision,platform,status,observed_version,last_job_id,reference_count,error_code,observed_at,version,created_at,updated_at"

func scanPlugin(row interface{ Scan(...any) error }) (pluginInstallation, error) {
	var i pluginInstallation
	err := row.Scan(&i.ID, &i.TargetID, &i.Key, &i.PluginVersion, &i.Kind, &i.InstallMode, &i.ImageRef, &i.ImageDigest, &i.ImageID, &i.RecipeRevision, &i.Platform, &i.Status, &i.ObservedVersion, &i.LastJobID, &i.ReferenceCount, &i.ErrorCode, &i.ObservedAt, &i.Version, &i.CreatedAt, &i.UpdatedAt)
	return i, err
}

func (s *Store) readPlugin(id string) (pluginInstallation, error) {
	i, e := scanPlugin(s.DB.QueryRow("SELECT "+pluginColumns+" FROM plugin_installations WHERE id=?", id))
	if e == nil {
		i.ReferenceCount, e = pluginReferences(s.DB, i)
	}
	return i, e
}

// P2 will add its environment relation query here when it has a consumer. P1
// keeps the explicit count reserved for the same-purpose reference guard.
func pluginReferences(q interface{ QueryRow(string, ...any) *sql.Row }, i pluginInstallation) (int, error) {
	var n int
	e := q.QueryRow("SELECT reference_count FROM plugin_installations WHERE id=?", i.ID).Scan(&n)
	return n, e
}

func pluginTargetSnapshot(t target) string {
	t.Observation = nil
	t.ObservedAt = ""
	return deploy.Digest(t)
}

func pluginRequest(d plugin.Definition) *deploy.PluginRequest {
	return &deploy.PluginRequest{Key: d.Key, Version: d.Version, Kind: d.Kind, Platform: d.Platform, Image: d.Image, Digest: d.Digest, RecipeRevision: d.RecipeRevision, Command: d.Command, Argument: d.Argument}
}

func installationDefinition(i pluginInstallation) (plugin.Definition, error) {
	d, err := plugin.Find(i.Key, i.PluginVersion)
	if err != nil || d.Digest != i.ImageDigest || d.RecipeRevision != i.RecipeRevision || d.Image != i.ImageRef || d.Kind != i.Kind || d.Platform != i.Platform {
		return plugin.Definition{}, errors.New("PLUGIN_MANIFEST_UNTRUSTED")
	}
	return d, nil
}
