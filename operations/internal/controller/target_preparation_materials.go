package controller

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (s *Store) preparationMaterialPath(binarySHA, scriptSHA string) string {
	return filepath.Join(s.Dir, "preparation-materials", hashToken(binarySHA+"\n"+scriptSHA))
}

func targetPackageMatches(material verifiedTargetPackage, binarySHA, scriptSHA string) bool {
	return material.Binary.Hash == binarySHA && material.Script.Hash == scriptSHA
}

func (h *HTTP) recoveryPreparation(t target, snapshot preparationSnapshot) (hostPreparation, verifiedTargetPackage, error) {
	directory := h.targetPackageDir
	if snapshot.RecoveryBinarySHA != "" {
		material, err := h.store.frozenPreparationMaterial(h.targetPackageDir, snapshot.RecoveryBinarySHA, snapshot.RecoveryScriptSHA)
		if err != nil {
			p, _ := preparationForHost(t, h.store.controllerID(), directory)
			p.Package.Available, p.Package.DownloadURL = false, ""
			p.Package.SHA256, p.Package.ByteSize = "", 0
			p.Package.Code, p.Package.Message = err.Error(), preparationMessage(err.Error())
			for i := range p.Steps {
				if p.Steps[i].Key == "package" {
					p.Steps[i].Status, p.Steps[i].Code, p.Steps[i].Message = "UNAVAILABLE", p.Package.Code, p.Package.Message
				}
			}
			return p, verifiedTargetPackage{}, err
		}
		directory = filepath.Dir(material.Binary.Path)
	}
	p, material := preparationForHost(t, h.store.controllerID(), directory)
	p.CommandPublicKey, p.RepairSHA = h.store.commandPublicKey(), snapshot.BinarySHA
	return p, material, nil
}

// Recovery pins both files, independently of the controller's current release.
func (s *Store) frozenPreparationMaterial(primary, binarySHA, scriptSHA string) (verifiedTargetPackage, error) {
	if !validSHA256(binarySHA) || !validSHA256(scriptSHA) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_RECOVERY_MATERIAL_DAMAGED")
	}
	if material, err := readTargetPackage(primary); err == nil && targetPackageMatches(material, binarySHA, scriptSHA) {
		return material, nil
	}
	root := filepath.Join(s.Dir, "preparation-materials")
	if info, err := os.Lstat(root); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_RECOVERY_MATERIAL_DAMAGED")
	}
	path := s.preparationMaterialPath(binarySHA, scriptSHA)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_RECOVERY_MATERIAL_MISSING")
	}
	material, err := readTargetPackage(path)
	if err != nil || !targetPackageMatches(material, binarySHA, scriptSHA) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_RECOVERY_MATERIAL_DAMAGED")
	}
	return material, nil
}

func (s *Store) cachePreparationMaterial(material verifiedTargetPackage) (verifiedTargetPackage, error) {
	if !validSHA256(material.Binary.Hash) || !validSHA256(material.Script.Hash) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	root := filepath.Join(s.Dir, "preparation-materials")
	if err := os.MkdirAll(root, 0700); err != nil {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	if err := privatePath(root, true); err != nil {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	destination := s.preparationMaterialPath(material.Binary.Hash, material.Script.Hash)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return s.frozenPreparationMaterial("", material.Binary.Hash, material.Script.Hash)
	}
	stage, err := os.MkdirTemp(root, ".new-")
	if err != nil {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	defer os.RemoveAll(stage)
	for _, item := range []targetPackageFile{material.Binary, material.Script} {
		if err = copyPreparationMaterial(item, filepath.Join(stage, item.Name)); err != nil {
			return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
		}
	}
	manifest := fmt.Sprintf("%s  streamfusion-ops\n%s  install-target.sh\n", material.Binary.Hash, material.Script.Hash)
	if err = writeExclusive(filepath.Join(stage, "SHA256SUMS"), []byte(manifest)); err != nil {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	if cached, e := readTargetPackage(stage); e != nil || !targetPackageMatches(cached, material.Binary.Hash, material.Script.Hash) {
		return verifiedTargetPackage{}, errors.New("PREPARATION_MATERIAL_CACHE_FAILED")
	}
	if err = os.Rename(stage, destination); err != nil {
		// Another request may have published the exact same pair first.
		return s.frozenPreparationMaterial("", material.Binary.Hash, material.Script.Hash)
	}
	return s.frozenPreparationMaterial("", material.Binary.Hash, material.Script.Hash)
}

func copyPreparationMaterial(item targetPackageFile, destination string) error {
	info, err := os.Lstat(item.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.Size {
		return errors.New("TARGET_PACKAGE_CHANGED")
	}
	source, err := os.Open(item.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(output, io.LimitReader(source, item.Size+1))
	if err == nil && n != item.Size {
		err = errors.New("TARGET_PACKAGE_CHANGED")
	}
	if err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}
