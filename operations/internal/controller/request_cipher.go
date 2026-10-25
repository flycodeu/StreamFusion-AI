package controller

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const requestCipherInfo = "streamfusion-ops-sensitive-v1"
const requestChallengeTTL = 2 * time.Minute
const maxRequestChallenges = 512
const maxSessionChallenges = 8

var errSecureRequest = errors.New("SECURE_REQUEST_INVALID")
var errChallengeLimit = errors.New("SECURE_REQUEST_LIMIT")

type requestChallenge struct {
	owner, path string
	key         *ecdh.PrivateKey
	expires     time.Time
}

// Request keys are ephemeral and independent of the on-disk credential master key.
// Cleanup is lazy and bounded; restarting the controller invalidates all challenges.
type requestCipher struct {
	mu         sync.Mutex
	challenges map[string]requestChallenge
}

type publicRequestChallenge struct {
	ID        string `json:"challengeId"`
	PublicKey string `json:"serverPublicKey"`
}

type encryptedRequest struct {
	ChallengeID     string `json:"challengeId"`
	ClientPublicKey string `json:"clientPublicKey"`
	IV              string `json:"iv"`
	Ciphertext      string `json:"ciphertext"`
}

func sensitiveRequestPath(path string) bool {
	if path == "/auth/login" || path == "/auth/password" || path == "/credentials" {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "credentials" || parts[3] != "revisions" {
		return false
	}
	_, err := uuid.Parse(parts[2])
	return err == nil
}

func (c *requestCipher) issue(owner, path string, now time.Time) (publicRequestChallenge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for id, item := range c.challenges {
		if !now.Before(item.expires) {
			delete(c.challenges, id)
		} else if item.owner == owner {
			count++
		}
	}
	if count >= maxSessionChallenges || len(c.challenges) >= maxRequestChallenges {
		return publicRequestChallenge{}, errChallengeLimit
	}
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return publicRequestChallenge{}, err
	}
	id := randomToken()
	if c.challenges == nil {
		c.challenges = make(map[string]requestChallenge)
	}
	c.challenges[id] = requestChallenge{owner, path, key, now.Add(requestChallengeTTL)}
	return publicRequestChallenge{id, base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}, nil
}

func requestAAD(id, path string) []byte {
	return []byte(requestCipherInfo + "\nPOST\n" + path + "\n" + id)
}

func (c *requestCipher) open(owner, path string, input encryptedRequest, now time.Time) ([]byte, error) {
	c.mu.Lock()
	item, ok := c.challenges[input.ChallengeID]
	// A different session must not be able to consume another session's challenge.
	if ok && item.owner == owner {
		delete(c.challenges, input.ChallengeID)
	}
	c.mu.Unlock()
	if !ok || item.owner != owner || item.path != path || !now.Before(item.expires) {
		return nil, errSecureRequest
	}
	if len(input.ClientPublicKey) != 87 || len(input.IV) != 16 || len(input.Ciphertext) > 87403 {
		return nil, errSecureRequest
	}
	public, err := base64.RawURLEncoding.Strict().DecodeString(input.ClientPublicKey)
	if err != nil || len(public) != 65 {
		return nil, errSecureRequest
	}
	peer, err := ecdh.P256().NewPublicKey(public)
	if err != nil {
		return nil, errSecureRequest
	}
	shared, err := item.key.ECDH(peer)
	if err != nil {
		return nil, errSecureRequest
	}
	defer clear(shared)
	key, err := hkdf.Key(sha256.New, shared, []byte(input.ChallengeID), requestCipherInfo, 32)
	if err != nil {
		return nil, errSecureRequest
	}
	defer clear(key)
	iv, err := base64.RawURLEncoding.Strict().DecodeString(input.IV)
	if err != nil || len(iv) != 12 {
		return nil, errSecureRequest
	}
	encrypted, err := base64.RawURLEncoding.Strict().DecodeString(input.Ciphertext)
	if err != nil || len(encrypted) < 17 || len(encrypted) > (64<<10)+16 {
		return nil, errSecureRequest
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errSecureRequest
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errSecureRequest
	}
	plaintext, err := gcm.Open(nil, iv, encrypted, requestAAD(input.ChallengeID, path))
	if err != nil {
		return nil, errSecureRequest
	}
	return plaintext, nil
}

func (h *HTTP) secureChallenge(w http.ResponseWriter, r *http.Request, s *session) {
	var input struct {
		Path string `json:"path"`
	}
	if decode(w, r, &input) != nil || !sensitiveRequestPath(input.Path) {
		h.fail(w, r, 400, "INVALID_INPUT", "不支持的敏感请求")
		return
	}
	if input.Path != "/auth/login" && s.AdminID == "" {
		h.fail(w, r, 401, "AUTH_REQUIRED", "请先登录")
		return
	}
	challenge, err := h.requestCipher.issue(s.Hash, input.Path, time.Now())
	if errors.Is(err, errChallengeLimit) {
		h.fail(w, r, 429, "SECURE_REQUEST_LIMIT", "安全请求过多，请稍后重试")
	} else if err != nil {
		h.internal(w, r, err)
	} else {
		h.success(w, r, challenge)
	}
}

// Only the encrypted envelope is accepted; there is no plaintext compatibility path.
func (h *HTTP) decodeSensitive(w http.ResponseWriter, r *http.Request, s *session, dst any) error {
	var input encryptedRequest
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errSecureRequest
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
		return errSecureRequest
	}
	plaintext, err := h.requestCipher.open(s.Hash, strings.TrimPrefix(r.URL.Path, api), input, time.Now())
	if err != nil {
		return errSecureRequest
	}
	defer clear(plaintext)
	d = json.NewDecoder(bytes.NewReader(plaintext))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		return errSecureRequest
	}
	return nil
}

func (h *HTTP) rejectSecureRequest(w http.ResponseWriter, r *http.Request) {
	h.logger.Printf("traceId=%s event=SECURE_REQUEST_REJECTED", r.Context().Value(traceKey{}))
	h.fail(w, r, 400, "SECURE_REQUEST_INVALID", "安全请求无效或已过期，请重新提交")
}
