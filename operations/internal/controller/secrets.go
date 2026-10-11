package controller

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

func seal(key []byte, id string, revision int, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plaintext, []byte(id+":"+strconv.Itoa(revision))), nil
}
func unseal(key []byte, id string, revision int, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < g.NonceSize() {
		return nil, errors.New("CREDENTIAL_DAMAGED")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], []byte(id+":"+strconv.Itoa(revision)))
}
func (s *Store) credentialFile(material string) string {
	return filepath.Join(s.Dir, "secrets", material)
}
func (s *Store) verifySecrets() error {
	rows, err := s.DB.Query("SELECT id,revision,material FROM credential_refs")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, material string
		var revision int
		if err = rows.Scan(&id, &revision, &material); err != nil {
			return err
		}
		if material != id+"-"+strconv.Itoa(revision)+".enc" {
			return errors.New("CREDENTIAL_PATH_INVALID")
		}
		path := s.credentialFile(material)
		if err = privatePath(path, false); err != nil {
			return err
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if _, e = unseal(s.Key, id, revision, data); e != nil {
			return errors.New("KEY_MISMATCH_OR_CREDENTIAL_DAMAGED")
		}
	}
	return rows.Err()
}
