package deploy

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"time"
)

// Export only an existing, verified built-in Java21 runtime image. This is a
// non-publishing target operation: the existing bounded version-check container
// exits with --rm, and image-save changes no image, package install or durable
// job state. A lost transfer can be safely requested again.
func (x *executor) exportRuntime(req Request, output io.Writer) error {
	definition, err := requestPluginDefinition(req)
	if err != nil || definition.Key != "java-runtime" || definition.Version != "21-jre" || !ID.MatchString(req.PluginInstallationID) {
		return errors.New("JAVA21_ENVIRONMENT_REQUIRED")
	}
	lock, err := x.handoffLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	var installed pluginMaterial
	if readJSON(filepath.Join(x.root, "plugins", req.PluginInstallationID+".json"), &installed) != nil || installed.InstallationID != req.PluginInstallationID || installed.Definition != definition {
		return errors.New("RUNTIME_INSTALLATION_REQUIRED")
	}
	actual, err := x.probePlugin(definition)
	if err != nil || actual.ImageID != installed.ImageID || actual.Health != "PLUGIN_READY" {
		return errors.New("RUNTIME_ACTUAL_STATE_DRIFT")
	}
	if err := secureAncestors(x.owner.DockerBinary); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, x.owner.DockerBinary, "--host", "unix://"+x.owner.DockerSocket, "image", "save", installed.ImageID)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root", "DOCKER_CONFIG=" + filepath.Join(x.root, "docker-config")}
	cmd.Stdout = output
	cmd.Stderr = &cappedBuffer{limit: 1024}
	if err := cmd.Run(); err != nil {
		return errors.New("RUNTIME_EXPORT_FAILED")
	}
	return nil
}
