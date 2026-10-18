package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func (h *HTTP) processJarImport(id string, in importInput) {
	fail := func(code string) {
		if strings.ContainsAny(code, ":\r\n") || !(strings.HasPrefix(code, "JAR_") || strings.HasPrefix(code, "JAVA21_") || strings.HasPrefix(code, "RUNTIME_") || strings.HasPrefix(code, "ARCHIVE_") || code == "ARTIFACT_COMMIT_FAILED" || code == "TARGET_MISSING" || code == "TARGET_JAR_RECIPE_REQUIRED") {
			code = "JAR_PACKAGE_FAILED"
		}
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		_, _ = h.store.DB.Exec("UPDATE jobs SET status='FAILED',error_code=?,updated_at=? WHERE id=? AND status='RUNNING'", code, Now(), id)
	}
	if in.RuntimeEnvironment == nil {
		fail("JAVA21_ENVIRONMENT_REQUIRED")
		return
	}
	if err := deploy.ValidateJavaJar(h.store.uploadPath(id), in.SHA, in.Size); err != nil {
		fail(err.Error())
		return
	}
	installation, err := h.store.readPlugin(in.PluginInstallationID)
	if err != nil || installation.Status != "READY" || installation.ImageID != in.RuntimeEnvironment.ImageID || installation.ImageDigest != in.RuntimeEnvironment.ImageDigest {
		fail("JAVA21_ENVIRONMENT_CHANGED")
		return
	}
	target, err := h.store.readTarget(installation.TargetID)
	if err != nil {
		fail("TARGET_MISSING")
		return
	}
	capabilities, err := h.probe(target)
	var fact struct {
		JavaJarRecipe string `json:"javaJarRecipe"`
	}
	if err != nil {
		fail("RUNTIME_EXPORT_UNAVAILABLE")
		return
	}
	if json.Unmarshal(capabilities, &fact) != nil || fact.JavaJarRecipe != "java-jar-v1" {
		fail("TARGET_JAR_RECIPE_REQUIRED")
		return
	}
	base := filepath.Join(h.store.Dir, "packages", id+".runtime.partial")
	defer os.Remove(base)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := h.store.exportRuntime(ctx, target, installation, base); err != nil {
		fail("RUNTIME_EXPORT_UNAVAILABLE")
		return
	}
	artifactID := uuid.NewString()
	output := h.store.artifactPath(artifactID)
	registered := false
	defer func() {
		if !registered {
			os.Remove(output)
		}
	}()
	image, sha, size, err := deploy.PackageJavaJar(base, h.store.uploadPath(id), output, installation.ImageID, in.SHA, in.Size, id, *in.RuntimeEnvironment)
	if err != nil {
		fail(err.Error())
		return
	}
	h.store.mu.Lock()
	err = func() error {
		tx, err := h.store.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var status string
		if err := tx.QueryRow("SELECT status FROM jobs WHERE id=?", id).Scan(&status); err != nil {
			return err
		}
		if status != "RUNNING" {
			return errors.New("JAR_JOB_STATE_CHANGED")
		}
		if _, err = tx.Exec("INSERT INTO artifacts(id,name,archive_sha256,byte_size,image_id,created_at,recipe,runtime_environment_json) VALUES(?,?,?,?,?,?,?,?)", artifactID, in.Name, sha, size, image, Now(), "java-compose-v1", jsonText(in.RuntimeEnvironment)); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE jobs SET status='SUCCEEDED',phase='REGISTERED',result_json=?,updated_at=? WHERE id=?", jsonText(map[string]any{"artifactId": artifactID, "imageId": image, "runtimeEnvironment": in.RuntimeEnvironment}), Now(), id); err != nil {
			return err
		}
		if err = auditTx(tx, "worker", "JAR_PACKAGED", id, "SUCCESS", id); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err == nil {
		registered = true
	} else {
		var count int
		// If the local database cannot confirm the commit outcome, retain the
		// valid archive for diagnosis rather than deleting a possibly committed artifact.
		check := h.store.DB.QueryRow("SELECT count(*) FROM artifacts WHERE id=?", artifactID).Scan(&count)
		registered = check != nil || count == 1
	}
	h.store.mu.Unlock()
	if err != nil {
		h.logger.Printf("jobId=%s artifactId=%s jarPackageCommit=FAILED", id, artifactID)
		fail("ARTIFACT_COMMIT_FAILED")
	} else {
		_ = os.Remove(h.store.uploadPath(id))
	}
}
