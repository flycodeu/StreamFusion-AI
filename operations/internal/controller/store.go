package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const Version = "0.2.0"
const SchemaVersion = 5

//go:embed schema.sql
var schema string

type Store struct {
	DB   *sql.DB
	Dir  string
	Key  []byte
	lock *os.File
	mu   sync.Mutex // Serialize writes spanning SQLite and protected files.
}

func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hashToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func privatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return fmt.Errorf("UNSAFE_PATH: %s", path)
	}
	if directory {
		return os.Chmod(path, 0700)
	}
	return os.Chmod(path, 0600)
}

func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, fmt.Errorf("STATE_DIRECTORY: %w", err)
	}
	if err = privatePath(abs, true); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(abs, "controller.lock")
	if _, err = os.Lstat(lockPath); err == nil {
		if err = privatePath(lockPath, false); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	s := &Store{Dir: abs, lock: f}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	dbPath := filepath.Join(abs, "controller.sqlite")
	if _, err = os.Lstat(dbPath); err == nil {
		if err = privatePath(dbPath, false); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.DB, err = sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	s.DB.SetMaxOpenConns(1)
	if _, err = s.DB.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}
	var version int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version > SchemaVersion {
		return nil, fmt.Errorf("SCHEMA_TOO_NEW: 数据库 %d，程序支持 %d", version, SchemaVersion)
	}
	if version == 0 {
		var tables int
		if err = s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return nil, err
		}
		if tables != 0 {
			return nil, errors.New("SCHEMA_UNKNOWN: 非空数据库未登记版本，拒绝重建")
		}
		tx, e := s.DB.Begin()
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		if _, e = tx.Exec(schema); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO controller_state(id,controller_id,name,created_at) VALUES(1,?,?,?)", uuid.NewString(), "StreamFusion Ops", Now()); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES(1,?,?,?)", hashToken(schema), Version, Now()); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("PRAGMA user_version=1"); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	var checksum string
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); err != nil || checksum != hashToken(schema) {
		return nil, errors.New("SCHEMA_MISMATCH: 迁移摘要不匹配")
	}
	if _, err = s.DB.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	if err = privatePath(dbPath, false); err != nil {
		return nil, err
	}
	secretDir := filepath.Join(abs, "secrets")
	if err = os.MkdirAll(secretDir, 0700); err != nil {
		return nil, err
	}
	if err = privatePath(secretDir, true); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(abs, "master.key")
	if _, err = os.Lstat(keyPath); err == nil {
		if err = privatePath(keyPath, false); err != nil {
			return nil, err
		}
		s.Key, err = os.ReadFile(keyPath)
	} else if os.IsNotExist(err) {
		var count int
		if err = s.DB.QueryRow("SELECT count(*) FROM credential_refs").Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, errors.New("KEY_MISSING: 存在凭据，拒绝生成替代密钥")
		}
		s.Key = make([]byte, 32)
		if _, err = rand.Read(s.Key); err == nil {
			err = writeExclusive(keyPath, s.Key)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(s.Key) != 32 {
		return nil, errors.New("KEY_INVALID: 密钥必须为32字节")
	}
	if err = s.verifySecrets(); err != nil {
		return nil, err
	}
	if version == 1 {
		backupPath := filepath.Join(abs, "before-schema-2-"+time.Now().UTC().Format("20060102T150405.000000000")+".tar.gz")
		if err = s.BackupTo(backupPath); err != nil {
			return nil, fmt.Errorf("SCHEMA_BACKUP_FAILED: %w", err)
		}
	}
	if version <= 1 {
		tx, e := s.DB.Begin()
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(schemaV2); e != nil {
			tx.Rollback()
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES(2,?,?,?)", hashToken(schemaV2), Version, Now()); e != nil {
			tx.Rollback()
			return nil, e
		}
		if _, e = tx.Exec("PRAGMA user_version=2"); e != nil {
			tx.Rollback()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=2").Scan(&checksum); err != nil || checksum != hashToken(schemaV2) {
		return nil, errors.New("SCHEMA_MISMATCH: 迁移2摘要不匹配")
	}
	if version > 0 && version <= 2 {
		backupPath := filepath.Join(abs, "before-schema-3-"+time.Now().UTC().Format("20060102T150405.000000000")+".tar.gz")
		if err = s.BackupTo(backupPath); err != nil {
			return nil, fmt.Errorf("SCHEMA_BACKUP_FAILED: %w", err)
		}
	}
	if version <= 2 {
		tx, e := s.DB.Begin()
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(schemaV3); e != nil {
			tx.Rollback()
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO schema_migrations VALUES(3,?,?,?)", hashToken(schemaV3), Version, Now()); e != nil {
			tx.Rollback()
			return nil, e
		}
		if _, e = tx.Exec("PRAGMA user_version=3"); e != nil {
			tx.Rollback()
			return nil, e
		}
		foreignKeys, e := tx.Query("PRAGMA foreign_key_check")
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		broken := foreignKeys.Next()
		e = foreignKeys.Err()
		foreignKeys.Close()
		if broken || e != nil {
			tx.Rollback()
			return nil, errors.New("SCHEMA_FOREIGN_KEY_INVALID")
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=3").Scan(&checksum); err != nil || checksum != hashToken(schemaV3) {
		return nil, errors.New("SCHEMA_MISMATCH: 迁移3摘要不匹配")
	}
	if version > 0 && version <= 3 {
		backupPath := filepath.Join(abs, "before-schema-4-"+time.Now().UTC().Format("20060102T150405.000000000")+".tar.gz")
		if err = s.BackupTo(backupPath); err != nil {
			return nil, fmt.Errorf("SCHEMA_BACKUP_FAILED: %w", err)
		}
	}
	if version <= 3 {
		tx, e := s.DB.Begin()
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(schemaV4); e == nil {
			_, e = tx.Exec("INSERT INTO schema_migrations VALUES(4,?,?,?)", hashToken(schemaV4), Version, Now())
		}
		if e == nil {
			_, e = tx.Exec("PRAGMA user_version=4")
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=4").Scan(&checksum); err != nil || checksum != hashToken(schemaV4) {
		return nil, errors.New("SCHEMA_MISMATCH: 迁移4摘要不匹配")
	}
	if version > 0 && version <= 4 {
		backupPath := filepath.Join(abs, "before-schema-5-"+time.Now().UTC().Format("20060102T150405.000000000")+".tar.gz")
		if err = s.BackupTo(backupPath); err != nil {
			return nil, fmt.Errorf("SCHEMA_BACKUP_FAILED: %w", err)
		}
	}
	if version <= 4 {
		// SQLite cannot widen an existing CHECK constraint in place. Disable
		// foreign-key actions only for this transaction, then verify all links.
		if _, err = s.DB.Exec("PRAGMA foreign_keys=OFF"); err != nil {
			return nil, err
		}
		tx, e := s.DB.Begin()
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(schemaV5); e == nil {
			_, e = tx.Exec("INSERT INTO schema_migrations VALUES(5,?,?,?)", hashToken(schemaV5), Version, Now())
		}
		if e == nil {
			rows, checkErr := tx.Query("PRAGMA foreign_key_check")
			if checkErr != nil {
				e = checkErr
			} else {
				if rows.Next() {
					e = errors.New("SCHEMA_FOREIGN_KEY_INVALID")
				} else {
					e = rows.Err()
				}
				rows.Close()
			}
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		if _, err = s.DB.Exec("PRAGMA foreign_keys=ON"); err != nil {
			return nil, err
		}
	}
	if err = s.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=5").Scan(&checksum); err != nil || checksum != hashToken(schemaV5) {
		return nil, errors.New("SCHEMA_MISMATCH: 迁移5摘要不匹配")
	}
	if _, err = s.DB.Exec("DELETE FROM sessions WHERE expires_at<=? OR last_seen<?", time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func (s *Store) Close() error {
	if s.DB != nil {
		s.DB.Close()
	}
	if s.lock != nil {
		return s.lock.Close()
	}
	return nil
}
func writeExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}
	return closeErr
}

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,31}$`)

func ValidatePassword(password string) error {
	if len(password) < 6 || len(password) > 64 {
		return errors.New("密码长度须为6–64字节")
	}
	var letter, digit bool
	for _, r := range password {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			letter = true
		}
		if r >= '0' && r <= '9' {
			digit = true
		}
	}
	if !letter || !digit {
		return errors.New("密码须包含英文字母和数字")
	}
	return nil
}

func (s *Store) InitAdmin(username, password string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return errors.New("账号须为3–32位小写字母、数字、点、横线或下划线，并以字母开头")
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM admins").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("ALREADY_INITIALIZED: 管理员已存在")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := uuid.NewString()
	if _, err = tx.Exec("INSERT INTO admins(id,username,password_hash,created_at) VALUES(?,?,?,?)", id, username, hash, Now()); err != nil {
		return err
	}
	if err = auditTx(tx, username, "ADMIN_INITIALIZED", id, "SUCCESS", ""); err != nil {
		return err
	}
	return tx.Commit()
}

func auditTx(tx *sql.Tx, actor, action, object, result, trace string) error {
	_, err := tx.Exec("INSERT INTO audit VALUES(?,?,?,?,?,?,?)", uuid.NewString(), actor, action, object, result, trace, Now())
	return err
}
