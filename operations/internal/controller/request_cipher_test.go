package controller

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func encryptRequestForTest(t *testing.T, challenge publicRequestChallenge, path string, plaintext []byte) encryptedRequest {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := base64.RawURLEncoding.DecodeString(challenge.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ecdh.P256().NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := private.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hkdf.Key(sha256.New, shared, []byte(challenge.ID), requestCipherInfo, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	if _, err = rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	return encryptedRequest{
		challenge.ID,
		base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(iv),
		base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, iv, plaintext, requestAAD(challenge.ID, path))),
	}
}

func (b *browser) encrypt(path string, data any) encryptedRequest {
	b.t.Helper()
	w := b.rawCall("POST", "/auth/secure-challenge", map[string]string{"path": path}, 200)
	d := dataOf(b.t, w)
	plaintext, err := json.Marshal(data)
	if err != nil {
		b.t.Fatal(err)
	}
	return encryptRequestForTest(b.t, publicRequestChallenge{d["challengeId"].(string), d["serverPublicKey"].(string)}, path, plaintext)
}

func TestRequestCipherBindingTamperingAndExpiry(t *testing.T) {
	for _, name := range []string{"success", "other-session", "other-path", "expired", "public-key", "iv", "ciphertext", "oversize"} {
		t.Run(name, func(t *testing.T) {
			var c requestCipher
			now := time.Now()
			challenge, err := c.issue("session-a", "/auth/login", now)
			if err != nil {
				t.Fatal(err)
			}
			plaintext := []byte(`{"username":"admin","password":"test-secret"}`)
			input := encryptRequestForTest(t, challenge, "/auth/login", plaintext)
			owner, path := "session-a", "/auth/login"
			switch name {
			case "other-session":
				owner = "session-b"
			case "other-path":
				path = "/auth/password"
			case "expired":
				now = now.Add(requestChallengeTTL)
			case "public-key":
				input.ClientPublicKey = base64.RawURLEncoding.EncodeToString(make([]byte, 65))
			case "iv":
				input.IV = "invalid"
			case "ciphertext":
				data, _ := base64.RawURLEncoding.DecodeString(input.Ciphertext)
				data[len(data)-1] ^= 1
				input.Ciphertext = base64.RawURLEncoding.EncodeToString(data)
			case "oversize":
				input = encryptRequestForTest(t, challenge, path, bytes.Repeat([]byte("x"), (64<<10)+1))
			}
			cleartext, err := c.open(owner, path, input, now)
			if name == "success" {
				if err != nil || !bytes.Equal(cleartext, plaintext) {
					t.Fatal("roundtrip failed", err)
				}
			} else if err != errSecureRequest || cleartext != nil {
				t.Fatal("invalid encrypted request accepted")
			}
			if name == "other-session" {
				if _, err := c.open("session-a", "/auth/login", input, now); err != nil {
					t.Fatal("other session consumed the challenge")
				}
			}
			if _, err := c.open("session-a", "/auth/login", input, now); err != errSecureRequest {
				t.Fatal("challenge replay accepted")
			}
		})
	}
}

func TestRequestCipherConcurrentSingleUse(t *testing.T) {
	var c requestCipher
	now := time.Now()
	challenge, err := c.issue("owner", "/credentials", now)
	if err != nil {
		t.Fatal(err)
	}
	input := encryptRequestForTest(t, challenge, "/credentials", []byte(`{"secret":"test-value"}`))
	var wg sync.WaitGroup
	var successes atomic.Int32
	for range 16 {
		wg.Go(func() {
			if _, err := c.open("owner", "/credentials", input, now); err == nil {
				successes.Add(1)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful uses=%d", successes.Load())
	}
}

func TestRequestCipherLimitsAndCleanup(t *testing.T) {
	var c requestCipher
	now := time.Now()
	for range maxSessionChallenges {
		if _, err := c.issue("owner", "/auth/login", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.issue("owner", "/auth/login", now); err != errChallengeLimit {
		t.Fatal("session limit missing")
	}
	if _, err := c.issue("owner", "/auth/login", now.Add(requestChallengeTTL)); err != nil || len(c.challenges) != 1 {
		t.Fatal("expired challenges not reclaimed")
	}
	for i := range maxRequestChallenges {
		c.challenges[string(rune(i))] = requestChallenge{owner: "other", expires: now.Add(time.Hour)}
	}
	if _, err := c.issue("fresh", "/auth/login", now); err != errChallengeLimit {
		t.Fatal("global limit missing")
	}
}

func TestSensitiveHTTPRejectsPlaintextAndDoesNotLeak(t *testing.T) {
	s, b := setup(t)
	var logs bytes.Buffer
	b.h.logger = log.New(&logs, "", 0)
	b.prepare()
	b.rawCall("POST", "/auth/secure-challenge", map[string]string{"path": "/credentials"}, 401)
	b.rawCall("POST", "/auth/secure-challenge", map[string]string{"path": "/settings"}, 400)
	b.rawCall("POST", "/auth/login", map[string]string{"username": "admin", "password": testPassword}, 400)
	var attempts int
	if err := s.DB.QueryRow("SELECT failed_attempts FROM admins").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("plaintext request reached password validation", err)
	}
	b.login()
	secret := "secret-must-not-leak-" + testPassword
	for _, path := range []string{"/auth/password", "/credentials", "/credentials/00000000-0000-0000-0000-000000000001/revisions"} {
		w := b.rawCall("POST", path, map[string]string{"secret": secret}, 400)
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("error leaked secret")
		}
	}
	d := dataOf(t, b.call("POST", "/credentials", map[string]string{"name": "test", "kind": "TOKEN", "secret": secret}, 201))
	if len(d) != 2 || d["id"] == nil || d["revision"] != "1" {
		t.Fatal("credential write returned unnecessary data")
	}
	for _, path := range []string{"/credentials", "/audit"} {
		if strings.Contains(b.call("GET", path, nil, 200).Body.String(), secret) {
			t.Fatal("read response leaked secret")
		}
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), testPassword) {
		t.Fatal("log leaked secret")
	}
}

func TestSensitiveHTTPReplayAndInvalidPayload(t *testing.T) {
	_, b := setup(t)
	b.prepare()
	input := b.encrypt("/auth/login", map[string]string{"username": "admin", "password": "incorrect-password"})
	b.rawCall("POST", "/auth/login", input, 401)
	b.rawCall("POST", "/auth/login", input, 400)
	other := &browser{t: t, h: b.h}
	other.prepare()
	input = b.encrypt("/auth/login", map[string]string{"username": "admin", "password": testPassword})
	other.rawCall("POST", "/auth/login", input, 400)
	b.rawCall("POST", "/auth/login", input, 200)
	// Session rotation also makes unused anonymous challenges unusable.
	b.prepare()
	stale := b.encrypt("/auth/password", map[string]string{"currentPassword": testPassword, "newPassword": "Next12"})
	b.call("POST", "/auth/logout", nil, 200)
	b.login()
	b.rawCall("POST", "/auth/password", stale, 400)
	for _, plaintext := range []string{`{"username":"admin","password":"unused","extra":"unused"}`, `{} {}`} {
		d := dataOf(t, b.rawCall("POST", "/auth/secure-challenge", map[string]string{"path": "/auth/login"}, 200))
		bad := encryptRequestForTest(t, publicRequestChallenge{d["challengeId"].(string), d["serverPublicKey"].(string)}, "/auth/login", []byte(plaintext))
		b.rawCall("POST", "/auth/login", bad, 400)
	}
}

func TestWebCryptoRequestInteroperability(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node 24 is required for browser Web Crypto interoperability")
	}
	_, b := setup(t)
	b.prepare()
	challenge := dataOf(t, b.rawCall("POST", "/auth/secure-challenge", map[string]string{"path": "/auth/login"}, 200))
	input, err := json.Marshal(map[string]any{"challenge": challenge, "path": "/auth/login", "data": map[string]string{"username": "admin", "password": testPassword}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("..", "..", "scripts", "encrypt-request.mjs"))
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	encrypted, err := cmd.Output()
	if err != nil {
		t.Fatalf("Web Crypto failed: %v %s", err, stderr.String())
	}
	var payload encryptedRequest
	if err := json.Unmarshal(encrypted, &payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(testPassword)) || bytes.Contains(encrypted, []byte(`"username"`)) {
		t.Fatal("browser payload contains plaintext credentials")
	}
	b.rawCall("POST", "/auth/login", payload, 200)
	b.call("GET", "/auth/me", nil, 200)
}

func TestSensitiveHTTPBodyLimit(t *testing.T) {
	_, b := setup(t)
	b.prepare()
	r := httptest.NewRequest("POST", "http://127.0.0.1:19090"+api+"/auth/login", io.NopCloser(strings.NewReader(strings.Repeat(" ", 96<<10)+"{}")))
	r.AddCookie(b.cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:19090")
	r.Header.Set("X-CSRF-Token", b.csrf)
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("oversized envelope accepted")
	}
}

func TestSecureChallengeWriteProtectionAndLimit(t *testing.T) {
	_, b := setup(t)
	request := map[string]string{"path": "/auth/login"}
	b.rawCall("POST", "/auth/secure-challenge", request, 401)
	b.prepare()
	csrf := b.csrf
	b.csrf = "wrong"
	b.rawCall("POST", "/auth/secure-challenge", request, 403)
	b.csrf = csrf
	for range maxSessionChallenges {
		w := b.rawCall("POST", "/auth/secure-challenge", request, 200)
		d := dataOf(t, w)
		if len(d) != 2 || d["challengeId"] == nil || d["serverPublicKey"] == nil {
			t.Fatal("challenge returned unnecessary fields")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("challenge can be cached")
		}
	}
	b.rawCall("POST", "/auth/secure-challenge", request, 429)
}
