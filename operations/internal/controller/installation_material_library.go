package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type MaterialLibraryImportItem struct {
	ID            string `json:"id"`
	RecipeKey     string `json:"recipeKey"`
	RecipeVersion string `json:"recipeVersion"`
	RuntimeMode   string `json:"runtimeMode"`
	Status        string `json:"status"`
}

type MaterialLibraryImportResult struct {
	Imported int                         `json:"imported"`
	Reused   int                         `json:"reused"`
	Skipped  int                         `json:"skipped"`
	Items    []MaterialLibraryImportItem `json:"items"`
}

// ImportInstallationMaterialLibrary imports release-sidecar bytes into the
// existing material store. It needs an initialized local administrator, never
// creates a session, contacts a target, downloads a file, or executes a package.
// Each successful item is durable; the result records progress if a later item
// fails. A subsequent invocation reuses verified records and resumes safely.
func (s *Store) ImportInstallationMaterialLibrary(ctx context.Context, dir string) (MaterialLibraryImportResult, error) {
	result := MaterialLibraryImportResult{Items: []MaterialLibraryImportItem{}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("MATERIAL_LIBRARY_DIRECTORY_INVALID")
	}
	manifest := filepath.Join(dir, "library.json")
	info, err = os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<20 {
		return result, errors.New("MATERIAL_LIBRARY_MANIFEST_INVALID")
	}
	f, err := os.Open(manifest)
	if err != nil {
		return result, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	var library serviceinstall.MaterialLibrary
	if err != nil || len(raw) > 1<<20 || deploy.StrictJSON(raw, &library) != nil || library.SchemaVersion != 1 || len(library.Materials) > 100 {
		return result, errors.New("MATERIAL_LIBRARY_MANIFEST_INVALID")
	}
	var total int64
	seen := map[string]bool{}
	for _, material := range library.Materials {
		if _, err = material.Definition(); err != nil {
			return result, err
		}
		key := serviceinstall.Digest(material)
		if seen[key] {
			return result, errors.New("MATERIAL_LIBRARY_DUPLICATE_ENTRY")
		}
		seen[key] = true
		total += material.ByteSize
		if total > 16<<30 {
			return result, errors.New("INSTALL_MATERIAL_QUOTA")
		}
	}
	var actorID string
	if err = s.DB.QueryRowContext(ctx, "SELECT id FROM admins ORDER BY created_at,id LIMIT 1").Scan(&actorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, errors.New("MATERIAL_LIBRARY_ADMIN_REQUIRED")
		}
		return result, err
	}
	for _, material := range library.Materials {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		m, outcome, importErr := s.importLibraryMaterial(ctx, dir, actorID, material)
		if importErr != nil {
			return result, fmt.Errorf("MATERIAL_LIBRARY_IMPORT_FAILED %s/%s: %w", material.RecipeKey, material.RecipeVersion, importErr)
		}
		switch outcome {
		case "IMPORTED":
			result.Imported++
		case "REUSED":
			result.Reused++
		case "DELETED":
			result.Skipped++
		}
		result.Items = append(result.Items, MaterialLibraryImportItem{ID: m.ID, RecipeKey: m.RecipeKey, RecipeVersion: m.RecipeVersion, RuntimeMode: m.RuntimeMode, Status: m.Status})
	}
	return result, nil
}

func (s *Store) importLibraryMaterial(ctx context.Context, dir, actorID string, input serviceinstall.LibraryMaterialFile) (installationMaterial, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := input.Definition()
	if err != nil {
		return installationMaterial{}, "", err
	}
	identity := serviceinstall.Digest(struct {
		Definition serviceinstall.Definition
		Mode       string
		SHA256     string
		ByteSize   int64
	}{d, input.RuntimeMode, input.SHA256, input.ByteSize})
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("streamfusion-material-library:"+identity)).String()
	existing, existingErr := s.readInstallationMaterial(id)
	if existingErr == nil && existing.Status == "DELETED" {
		// A library shipped beside the binary must not undo an explicit delete.
		return existing, "DELETED", nil
	}
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return installationMaterial{}, "", existingErr
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+materialColumns+" FROM installation_materials WHERE status='AVAILABLE' AND definition_json=? AND runtime_mode=? AND sha256=? AND byte_size=? ORDER BY created_at,id", jsonText(d), input.RuntimeMode, input.SHA256, input.ByteSize)
	if err != nil {
		return installationMaterial{}, "", err
	}
	candidates := []installationMaterial{}
	for rows.Next() {
		m, scanErr := scanInstallationMaterial(rows)
		if scanErr != nil {
			rows.Close()
			return installationMaterial{}, "", scanErr
		}
		candidates = append(candidates, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return installationMaterial{}, "", err
	}
	for _, m := range candidates {
		s.installationMaterialAvailability(&m)
		if !m.Available {
			continue
		}
		if _, checkErr := serviceinstall.ValidateMaterial(s.installationMaterialPath(m.ID, false), m.SHA256, m.ByteSize, d, m.RuntimeMode); checkErr == nil {
			s.installationMaterialOrigin(&m)
			return m, "REUSED", nil
		}
	}
	var count, active int
	var used int64
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(byte_size),0),coalesce(sum(CASE WHEN status IN ('UPLOADING','VERIFYING','DOWNLOADING') THEN 1 ELSE 0 END),0) FROM installation_materials WHERE status<>'DELETED' AND id<>?", id).Scan(&count, &used, &active); err != nil {
		return installationMaterial{}, "", err
	}
	if count >= 100 || used+input.ByteSize > 16<<30 {
		return installationMaterial{}, "", errors.New("INSTALL_MATERIAL_QUOTA")
	}
	if active >= 2 {
		return installationMaterial{}, "", errors.New("INSTALL_TRANSFER_BUSY")
	}
	storage := filepath.Dir(s.installationMaterialPath(id, false))
	if err = os.MkdirAll(storage, 0700); err == nil {
		err = privatePath(storage, true)
	}
	if err != nil {
		return installationMaterial{}, "", err
	}
	final := s.installationMaterialPath(id, false)
	// Recover an uncertain publish without replacing it. Corrupt published bytes
	// need an explicit delete instead of an unattended startup overwrite.
	_, statErr := os.Lstat(final)
	if statErr != nil && !os.IsNotExist(statErr) {
		return installationMaterial{}, "", statErr
	}
	image := ""
	if os.IsNotExist(statErr) {
		temp := filepath.Join(storage, ".library-"+uuid.NewString())
		defer os.Remove(temp)
		if err = serviceinstall.CopyLibraryMaterial(ctx, dir, temp, input); err == nil {
			image, err = serviceinstall.ValidateMaterial(temp, input.SHA256, input.ByteSize, d, input.RuntimeMode)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = os.Link(temp, final)
		}
	} else {
		info, checkErr := os.Lstat(final)
		if checkErr != nil || !info.Mode().IsRegular() {
			err = errors.New("MATERIAL_LIBRARY_PUBLISH_UNSAFE")
		} else {
			image, err = serviceinstall.ValidateMaterial(final, input.SHA256, input.ByteSize, d, input.RuntimeMode)
		}
	}
	if err != nil {
		return installationMaterial{}, "", err
	}
	if input.RuntimeMode == "NATIVE" {
		image = ""
	}
	now := Now()
	if err = s.writeMaterialOrigin(id, materialOrigin{Kind: "BUNDLED", ImportedAt: now, FileName: input.File}); err != nil {
		return installationMaterial{}, "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return installationMaterial{}, "", err
	}
	defer tx.Rollback()
	if errors.Is(existingErr, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, "INSERT INTO installation_materials(id,actor_id,name,definition_json,sha256,byte_size,status,image_id,platform,idempotency_key,request_hash,created_at,updated_at,runtime_mode) VALUES(?,?,?,?,?,?,'AVAILABLE',?,?,?,?,?,?,?)", id, actorID, input.Name, jsonText(d), input.SHA256, input.ByteSize, image, d.Platform, "library-"+identity, identity, now, now, input.RuntimeMode)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE installation_materials SET status='AVAILABLE',image_id=?,platform=?,error_code='',updated_at=? WHERE id=?", image, d.Platform, now, id)
	}
	if err == nil {
		err = auditTx(tx, "LOCAL_STARTUP", "INSTALL_MATERIAL_LIBRARY_IMPORTED", id, "SUCCESS", uuid.NewString())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return installationMaterial{}, "", err
	}
	m, err := s.readInstallationMaterial(id)
	return m, "IMPORTED", err
}
