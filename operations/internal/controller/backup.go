package controller

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const backupLimit = 256 << 20

type Manifest struct {
	SchemaVersion  int               `json:"schemaVersion"`
	ControllerID   string            `json:"controllerId"`
	CreatedAt      string            `json:"createdAt"`
	KeyFingerprint string            `json:"keyFingerprint"`
	Files          map[string]string `json:"files"`
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VACUUM INTO captures committed WAL data. The lock also freezes credential revisions.
func (s *Store) Backup() (string, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	temp, err := os.MkdirTemp(s.Dir, ".backup-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	snapshot := filepath.Join(temp, "controller.sqlite")
	if _, err = s.DB.Exec("VACUUM INTO '" + strings.ReplaceAll(filepath.ToSlash(snapshot), "'", "''") + "'"); err != nil {
		return "", nil, err
	}
	if err = privatePath(snapshot, false); err != nil {
		return "", nil, err
	}
	m := Manifest{SchemaVersion: SchemaVersion, CreatedAt: Now(), KeyFingerprint: hashToken(string(s.Key)), Files: map[string]string{}}
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&m.SchemaVersion); err != nil {
		return "", nil, err
	}
	if err = s.DB.QueryRow("SELECT controller_id FROM controller_state WHERE id=1").Scan(&m.ControllerID); err != nil {
		return "", nil, err
	}
	paths := map[string]string{"controller.sqlite": snapshot}
	rows, err := s.DB.Query("SELECT material FROM credential_refs")
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var material string
		if err = rows.Scan(&material); err != nil {
			break
		}
		if !secretName.MatchString(material) {
			err = errors.New("CREDENTIAL_PATH_INVALID")
			break
		}
		paths["secrets/"+material] = s.credentialFile(material)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	var total int64
	for name, path := range paths {
		if err = privatePath(path, false); err != nil {
			return "", nil, err
		}
		info, e := os.Stat(path)
		if e != nil {
			return "", nil, e
		}
		total += info.Size()
		if total > backupLimit {
			return "", nil, errors.New("BACKUP_TOO_LARGE: 超出256MiB底座备份限额")
		}
		m.Files[name], err = fileDigest(path)
		if err != nil {
			return "", nil, err
		}
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", nil, err
	}
	archive := filepath.Join(temp, "backup.tar.gz")
	f, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) error {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); e != nil {
			return e
		}
		_, e := tw.Write(data)
		return e
	}
	if err = add("manifest.json", manifest); err != nil {
		return "", nil, err
	}
	for name, path := range paths {
		info, e := os.Stat(path)
		if e != nil {
			return "", nil, e
		}
		if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return "", nil, err
		}
		src, e := os.Open(path)
		if e != nil {
			return "", nil, e
		}
		_, err = io.Copy(tw, src)
		src.Close()
		if err != nil {
			return "", nil, err
		}
	}
	if err = tw.Close(); err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		return "", nil, err
	}
	ok = true
	return archive, cleanup, nil
}
func (s *Store) BackupTo(out string) error {
	archive, cleanup, err := s.Backup()
	if err != nil {
		return err
	}
	defer cleanup()
	data, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	return writeExclusive(out, data)
}
func (h *HTTP) downloadBackup(w http.ResponseWriter, r *http.Request, s *session) {
	archive, cleanup, err := h.store.Backup()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	defer cleanup()
	h.store.mu.Lock()
	tx, err := h.beginAuthorized(s)
	if err == nil {
		err = auditTx(tx, s.Username, "BACKUP_CREATED", "controller", "SUCCESS", r.Context().Value(traceKey{}).(string))
		if err == nil {
			err = tx.Commit()
		}
		tx.Rollback()
	}
	h.store.mu.Unlock()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="streamfusion-ops-backup.tar.gz"`)
	http.ServeFile(w, r, archive)
}

var secretName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-[1-9][0-9]*\.enc$`)

func safeArchiveName(name string) bool {
	return name == "manifest.json" || name == "controller.sqlite" || strings.HasPrefix(name, "secrets/") && secretName.MatchString(strings.TrimPrefix(name, "secrets/"))
}

// Recovery is offline and only creates a new destination. No existing state is replaced.
func Restore(archive, keyPath, destination string) error {
	dest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("RESTORE_DESTINATION_EXISTS: 恢复路径必须不存在")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("KEY_INVALID")
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".ops-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = os.Mkdir(filepath.Join(stage, "secrets"), 0700); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(io.LimitReader(f, backupLimit+1))
	if err != nil {
		return err
	}
	defer gz.Close()
	decompressed := &io.LimitedReader{R: gz, N: backupLimit + (2 << 20)}
	tr := tar.NewReader(decompressed)
	files := map[string]string{}
	var total int64
	count := 0
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		count++
		total += header.Size
		if count > 502 || header.Size < 0 || total > backupLimit || header.Typeflag != tar.TypeReg || !safeArchiveName(header.Name) {
			return errors.New("BACKUP_INVALID_ENTRY")
		}
		if _, exists := files[header.Name]; exists {
			return errors.New("BACKUP_DUPLICATE_ENTRY")
		}
		if header.Name == "manifest.json" && header.Size > 1<<20 {
			return errors.New("MANIFEST_TOO_LARGE")
		}
		path := filepath.Join(stage, filepath.FromSlash(header.Name))
		dst, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = io.CopyN(dst, tr, header.Size)
		if e == nil {
			e = dst.Sync()
		}
		dst.Close()
		if e != nil {
			return e
		}
		files[header.Name], e = fileDigest(path)
		if e != nil {
			return e
		}
	}
	// Consume the gzip footer so damaged/truncated streams cannot pass verification.
	if _, err = io.Copy(io.Discard, decompressed); err != nil {
		return err
	}
	if decompressed.N == 0 {
		return errors.New("BACKUP_DECOMPRESSED_LIMIT")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(stage, "manifest.json"))
	if err != nil {
		return err
	}
	var m Manifest
	if err = json.Unmarshal(manifestBytes, &m); err != nil {
		return err
	}
	if m.SchemaVersion < 1 || m.SchemaVersion > SchemaVersion || m.KeyFingerprint != hashToken(string(key)) || len(m.Files)+1 != len(files) || m.Files["controller.sqlite"] == "" {
		return errors.New("BACKUP_MANIFEST_OR_KEY_MISMATCH")
	}
	for name, digest := range m.Files {
		if digest != files[name] || !safeArchiveName(name) || name == "manifest.json" {
			return errors.New("BACKUP_CHECKSUM_MISMATCH")
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(stage, "controller.sqlite"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	var integrity, id string
	var schemaVersion int
	err = db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if err == nil {
		err = db.QueryRow("PRAGMA user_version").Scan(&schemaVersion)
	}
	if err == nil {
		err = db.QueryRow("SELECT controller_id FROM controller_state WHERE id=1").Scan(&id)
	}
	if err == nil {
		var rows *sql.Rows
		rows, err = db.Query("PRAGMA foreign_key_check")
		if err == nil {
			if rows.Next() {
				err = errors.New("BACKUP_FOREIGN_KEY_ERROR")
			}
			rows.Close()
		}
	}
	db.Close()
	if err != nil {
		return err
	}
	if integrity != "ok" || schemaVersion != m.SchemaVersion || id != m.ControllerID {
		return errors.New("BACKUP_DATABASE_MISMATCH")
	}
	if err = writeExclusive(filepath.Join(stage, "master.key"), key); err != nil {
		return err
	}
	restored, err := Open(stage)
	if err != nil {
		return err
	}
	tx, err := restored.DB.Begin()
	if err == nil {
		if _, err = tx.Exec("DELETE FROM sessions; UPDATE admins SET session_generation=session_generation+1;"); err == nil {
			err = auditTx(tx, "local-cli", "STATE_RESTORED", id, "SUCCESS", "")
		}
		if err == nil {
			err = tx.Commit()
		}
		tx.Rollback()
	}
	restored.Close()
	if err != nil {
		return err
	}
	os.Remove(filepath.Join(stage, "manifest.json"))
	if _, err = os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("RESTORE_DESTINATION_EXISTS")
	}
	if err = publishState(stage, dest); err != nil {
		return fmt.Errorf("RESTORE_MOVE: %w", err)
	}
	return nil
}
