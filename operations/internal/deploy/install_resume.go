package deploy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Only replay preparation. Package application, service creation, and native
// ownership use the existing observation-based recovery instead.
func (x *executor) installResumeOffset(req Request, dir string) (int64, error) {
	unsafe := errors.New("INSTALL_RESUME_UNSAFE")
	i := req.Installation
	if i == nil || i.Definition.Commands != nil && !serviceinstall.IsBuiltinCommand(i.Definition) || serviceinstall.IsNode(i.Definition) || i.Definition.Key == "docker" && i.Config.Mode() == "NATIVE" && !i.Source.TargetDownload(i.Config) {
		return 0, unsafe
	}
	info, err := os.Lstat(dir)
	if err == nil && !info.IsDir() || err != nil && !os.IsNotExist(err) {
		return 0, unsafe
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return 0, unsafe
	}
	for _, path := range []string{x.softwareFile(i.SoftwareID), filepath.Join(x.root, "plugins", i.SoftwareID+".json")} {
		if _, err = os.Lstat(path); !os.IsNotExist(err) {
			return 0, unsafe
		}
	}
	if !serviceinstall.IsOfficial(i.Definition) {
		if _, err = os.Lstat(serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)); !os.IsNotExist(err) {
			return 0, unsafe
		}
	}
	var intent, pending Request
	intentErr := readJSON(filepath.Join(dir, "intent.json"), &intent)
	pendingErr := readJSON(filepath.Join(x.root, "pending.json"), &pending)
	if os.IsNotExist(intentErr) && os.IsNotExist(pendingErr) && len(entries) == 0 {
		return 0, nil
	}
	matches := func(record Request) bool {
		return record.JobID == req.JobID && record.InputHash == req.InputHash && validateSoftwareInput(record) == nil
	}
	if intentErr != nil || pendingErr != nil || !matches(intent) || !matches(pending) {
		return 0, unsafe
	}
	var result Result
	if err = readJSON(filepath.Join(dir, "result.json"), &result); err == nil {
		if result.JobID != req.JobID || result.InputHash != req.InputHash || result.Status != "UNKNOWN" {
			return 0, unsafe
		}
	} else if !os.IsNotExist(err) {
		return 0, unsafe
	}
	allowed := map[string]bool{"intent.json": true, "progress.json": true, "result.json": true, "service-install.log": true}
	archive := ""
	switch {
	case serviceinstall.IsOfficial(i.Definition):
		allowed["official-update.json"] = true
	case i.Source.TargetDownload(i.Config):
		// Downloaded bytes alone do not imply package or service mutation.
		// Every part is rehashed before extraction when this job continues.
	case i.Config.Mode() == "NATIVE":
		archive = "native-material"
		for _, part := range i.Definition.Native.Parts {
			allowed[part.Name] = true
		}
	case i.Source.Type == "ARCHIVE":
		archive = "service-image.partial"
	}
	for _, entry := range entries {
		if builtinFlow(req) && strings.HasPrefix(entry.Name(), "builtin-step-") {
			id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "builtin-step-"), ".json")
			step := builtinStep(req, id)
			var receipt commandStepReceipt
			if entry.Type() != 0 || step == nil || !replayableBuiltinPreparation(req, *step) || readJSON(filepath.Join(dir, entry.Name()), &receipt) != nil {
				return 0, unsafe
			}
			continue
		}
		if i.Source.TargetDownload(i.Config) && entry.Name() == "native-download" {
			if !entry.IsDir() || !nativeDownloadCacheSafe(filepath.Join(dir, entry.Name()), i.Definition) {
				return 0, unsafe
			}
			continue
		}
		if entry.Type() != 0 || !allowed[entry.Name()] && entry.Name() != archive {
			return 0, unsafe
		}
	}
	if archive == "" {
		return 0, nil
	}
	info, err = os.Lstat(filepath.Join(dir, archive))
	if os.IsNotExist(err) {
		if archive == "service-image.partial" {
			cached := filepath.Join(x.root, "packages", "service-"+i.Source.SHA256+".tar")
			if info, err = os.Lstat(cached); err == nil && info.Mode().IsRegular() && info.Size() == i.Source.ByteSize {
				return i.Source.ByteSize, nil
			}
		}
		return 0, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > i.Source.ByteSize {
		return 0, unsafe
	}
	return info.Size(), nil
}

func replayableBuiltinPreparation(req Request, step serviceinstall.CommandStep) bool {
	if step.Kind == "SYSTEM" {
		return step.Action == "INSTALL_MATERIAL"
	}
	if step.ID != "OFFICIAL_REFRESH" && step.ID != "SERVICE_IMAGE_PULL" {
		return false
	}
	for _, d := range serviceinstall.BuiltinCommandCatalog() {
		if d.Key != req.Installation.Definition.Key || d.Version != req.Installation.Definition.Version {
			continue
		}
		for _, original := range d.Commands.Steps {
			if original.ID == step.ID {
				return reflect.DeepEqual(original, step)
			}
		}
	}
	return false
}

// Called only after installResumeOffset proves that no mutation step started.
// The prior attempt stays in the installation log; a new receipt tracks retry.
func (x *executor) prepareBuiltinResume(req Request, dir string) error {
	if !builtinFlow(req) {
		return nil
	}
	for _, step := range req.Installation.Definition.Commands.Steps {
		if !replayableBuiltinPreparation(req, step) {
			continue
		}
		file := filepath.Join(dir, "builtin-step-"+step.ID+".json")
		var receipt commandStepReceipt
		if err := readJSON(file, &receipt); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		x.appendServiceLog(req, step.ID, "继续准备步骤，上次状态："+receipt.State+"，开始于 "+receipt.StartedAt)
		if err := os.Remove(file); err != nil {
			return err
		}
	}
	return nil
}

func nativeDownloadCacheSafe(directory string, d serviceinstall.Definition) bool {
	entries, err := os.ReadDir(directory)
	if err != nil || d.Native == nil {
		return false
	}
	expected := make(map[string]int64, len(d.Native.Parts))
	for _, part := range d.Native.Parts {
		expected[part.Name] = part.ByteSize
	}
	var total int64
	for _, entry := range entries {
		limit, exists := expected[entry.Name()]
		info, err := entry.Info()
		if !exists || err != nil || !info.Mode().IsRegular() || limit > 0 && info.Size() > limit {
			return false
		}
		total += info.Size()
	}
	return total <= serviceinstall.ArchiveLimit
}

func temporaryInstallNetwork(output []byte) bool {
	message := strings.ToLower(string(output))
	for _, fragment := range []string{"temporary failure resolving", "connection timed out", "could not connect", "connection reset by peer", "network is unreachable", "i/o timeout", "tls handshake timeout"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

// The caller revalidates the entire archive against the frozen digest before
// extracting or loading it, including bytes written in the earlier attempt.
func receiveInstallArchive(file string, input io.Reader, size, offset int64) error {
	info, err := os.Lstat(file)
	if err == nil && !info.Mode().IsRegular() || err != nil && !os.IsNotExist(err) {
		return errors.New("INSTALL_RESUME_OFFSET_CHANGED")
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || info.Size() != offset || offset > size {
		return errors.New("INSTALL_RESUME_OFFSET_CHANGED")
	}
	if offset < size {
		if _, err = io.CopyN(f, input, size-offset); err != nil {
			_ = f.Sync()
			return err
		}
	}
	return f.Sync()
}
