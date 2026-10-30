package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (x *executor) waitServiceHealth(req Request, s SoftwareRequest, timeout time.Duration) (string, error) {
	if err := x.checkpoint(req, "SERVICE_HEALTH"); err != nil {
		return "", err
	}
	if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil && req.Action == "SOFTWARE_INSTALL" {
		timeout = min(timeout, time.Until(deadline))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		if version, err := x.serviceHealth(ctx, req, s); err == nil {
			x.appendServiceLog(req, "SERVICE_HEALTH", "服务版本、身份与协议检查通过")
			return version, nil
		}
		select {
		case <-ctx.Done():
			x.appendServiceLog(req, "SERVICE_HEALTH", "SERVICE_HEALTH_TIMEOUT：协议检查未通过，请查看运行日志")
			return "", errors.New("SERVICE_HEALTH_TIMEOUT")
		case <-time.After(time.Second):
		}
	}
}

func (x *executor) serviceHealth(ctx context.Context, req Request, s SoftwareRequest) (string, error) {
	i := req.Installation
	if i.Config.Mode() == "NATIVE" {
		return x.nativeServiceHealth(ctx, req, s)
	}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State != "RUNNING" {
		return "", errors.New("SERVICE_NOT_RUNNING")
	}
	secret, _, accessKey, err := x.serviceSecrets(*i)
	if err != nil {
		return "", err
	}
	var command []string
	switch i.Definition.Key {
	case "mysql":
		// The vendor entrypoint temporarily starts MySQL without networking.
		// PID1 must have exec'd the final mysqld before authentication can be
		// accepted as installed, otherwise initialization can still be underway.
		out, runErr := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "exec", s.Locator, "cat", "/proc/1/comm")
		if runErr != nil || strings.TrimSpace(string(out)) != "mysqld" {
			return "", errors.New("SERVICE_INITIALIZING")
		}
		command = []string{"mysql", "--defaults-extra-file=/run/secrets/mysql-health.cnf", "--batch", "--skip-column-names", "--execute", "SELECT VERSION()"}
	case "redis":
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		conn, dialErr := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(probeCtx, "tcp", net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)))
		if dialErr != nil {
			cancel()
			return "", errors.New("SERVICE_PROTOCOL_UNREACHABLE")
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		err = probeRedis(conn, Binding{CredentialID: i.Config.CredentialID}, secret)
		conn.Close()
		cancel()
		if err != nil {
			return "", errors.New("SERVICE_AUTHENTICATION_FAILED")
		}
		command = []string{"redis-server", "--version"}
	case "nginx":
		body, getErr := serviceHTTP(ctx, "http://"+net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort))+"/__sfops_health", nil)
		if getErr != nil || string(body) != i.SoftwareID {
			return "", errors.New("SERVICE_SITE_HEALTH_FAILED")
		}
		if _, testErr := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "exec", s.Locator, "nginx", "-t"); testErr != nil {
			return "", errors.New("SERVICE_CONFIG_TEST_FAILED")
		}
		command = []string{"nginx", "-v"}
	case "seaweedfs":
		if err = serviceS3Authentication(ctx, net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)), accessKey, secret); err != nil {
			return "", err
		}
		command = []string{"/usr/bin/weed", "version"}
	case "mediamtx":
		if err = checkMediaMTX(ctx, *i, secret); err != nil {
			return "", err
		}
		command = []string{"/mediamtx", "--version"}
	case "zlm":
		if err = checkZLM(ctx, *i, secret); err != nil {
			return "", err
		}
		command = []string{"/opt/media/bin/MediaServer", "--version"}
	default:
		return "", errors.New("SERVICE_RECIPE_UNSUPPORTED")
	}
	out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", append([]string{"container", "exec", s.Locator}, command...)...)
	if err != nil || len(strings.TrimSpace(string(out))) == 0 || i.Definition.Key != "zlm" && !strings.Contains(string(out), i.Definition.Version) {
		return "", errors.New("SERVICE_VERSION_MISMATCH")
	}
	return strings.TrimSpace(string(out)), nil
}

func serviceHTTP(ctx context.Context, address string, headers map[string]string) ([]byte, error) {
	status, body, err := serviceHTTPResponse(ctx, address, headers)
	if err != nil || status != http.StatusOK {
		return nil, errors.New("SERVICE_PROTOCOL_RESPONSE_INVALID")
	}
	return body, nil
}

func serviceHTTPResponse(ctx context.Context, address string, headers map[string]string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, errors.New("SERVICE_PROTOCOL_UNREACHABLE")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return response.StatusCode, nil, errors.New("SERVICE_PROTOCOL_RESPONSE_INVALID")
	}
	return response.StatusCode, body, nil
}

func serviceS3Authentication(ctx context.Context, endpoint, accessKey, secret string) error {
	now := time.Now().UTC()
	date, amzDate := now.Format("20060102"), now.Format("20060102T150405Z")
	empty := sha256.Sum256(nil)
	payload := hex.EncodeToString(empty[:])
	canonical := "GET\n/\n\nhost:" + endpoint + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + amzDate + "\n\nhost;x-amz-content-sha256;x-amz-date\n" + payload
	hash := sha256.Sum256([]byte(canonical))
	scope := date + "/us-east-1/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(hash[:])
	sign := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		_, _ = h.Write([]byte(value))
		return h.Sum(nil)
	}
	key := sign([]byte("AWS4"+secret), date)
	key = sign(key, "us-east-1")
	key = sign(key, "s3")
	key = sign(key, "aws4_request")
	headers := map[string]string{"X-Amz-Date": amzDate, "X-Amz-Content-Sha256": payload, "Authorization": fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=%s", accessKey, scope, hex.EncodeToString(sign(key, toSign)))}
	body, err := serviceHTTP(ctx, "http://"+endpoint+"/", headers)
	var result struct{ XMLName xml.Name }
	if err != nil || xml.Unmarshal(body, &result) != nil || result.XMLName.Local != "ListAllMyBucketsResult" {
		return errors.New("SERVICE_S3_AUTHENTICATION_FAILED")
	}
	// A positive request alone also succeeds against a mistakenly anonymous
	// S3 endpoint. This install-only negative check proves authentication is
	// enforced, without mutating objects or storing either credential.
	last := headers["Authorization"][len(headers["Authorization"])-1]
	replacement := byte('0')
	if last == replacement {
		replacement = '1'
	}
	headers["Authorization"] = headers["Authorization"][:len(headers["Authorization"])-1] + string(replacement)
	status, _, rejectErr := serviceHTTPResponse(ctx, "http://"+endpoint+"/", headers)
	if rejectErr != nil || status != http.StatusForbidden {
		return errors.New("SERVICE_S3_AUTHENTICATION_NOT_ENFORCED")
	}
	return nil
}

func checkMediaMTX(ctx context.Context, i serviceinstall.Request, secret string) error {
	user := i.Config.Username
	if user == "" {
		user = "ops"
	}
	endpoint := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(serviceinstall.PortValue(i.Definition, i.Config, "api"))) + "/v3/paths/list"
	body, err := serviceHTTP(ctx, endpoint, map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+secret))})
	var response map[string]json.RawMessage
	if err != nil || json.Unmarshal(body, &response) != nil || response["items"] == nil {
		return errors.New("MEDIAMTX_API_HEALTH_FAILED")
	}
	status, _, err := serviceHTTPResponse(ctx, endpoint, nil)
	if err != nil || status != 401 && status != 403 {
		return errors.New("MEDIAMTX_API_AUTHENTICATION_NOT_ENFORCED")
	}
	return nil
}
func checkZLM(ctx context.Context, i serviceinstall.Request, secret string) error {
	endpoint := "http://" + net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)) + "/index/api/getThreadsLoad"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"secret": []string{secret}}.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result, err := client.Do(request)
	if err != nil {
		return errors.New("ZLM_API_UNREACHABLE")
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, (64<<10)+1))
	var value struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err != nil || result.StatusCode != 200 || len(body) > 64<<10 || json.Unmarshal(body, &value) != nil || value.Code != 0 || len(value.Data) == 0 {
		return errors.New("ZLM_API_HEALTH_FAILED")
	}
	return nil
}
func (x *executor) nativeServiceHealth(ctx context.Context, req Request, s SoftwareRequest) (string, error) {
	i := req.Installation
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State != "RUNNING" {
		return "", errors.New("SERVICE_NOT_RUNNING")
	}
	secret, _, access, err := x.serviceSecrets(*i)
	if err != nil {
		return "", err
	}
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	binary := filepath.Join(root, nativeBinaryRelative(i.Definition))
	args := []string{"--version"}
	switch i.Definition.Key {
	case "mysql":
		out, err := softwareCommand(ctx, x.softwareRunner, filepath.Join(root, "runtime/mysql/bin/mysql"), "--defaults-extra-file="+filepath.Join(root, "config/health.cnf"), "--batch", "--skip-column-names", "--execute", "SELECT VERSION()")
		if err != nil || !strings.Contains(string(out), i.Definition.Version) {
			return "", errors.New("SERVICE_AUTHENTICATION_FAILED")
		}
		// Bootstrap is used only until the first successful authenticated startup.
		config := filepath.Join(root, "config/my.cnf")
		if content, readErr := os.ReadFile(config); readErr == nil && strings.Contains(string(content), "init-file=") {
			lines := []string{}
			for _, line := range strings.Split(string(content), "\n") {
				if !strings.HasPrefix(line, "init-file=") {
					lines = append(lines, line)
				}
			}
			if err = replaceProtectedServiceFile(config, []byte(strings.Join(lines, "\n"))); err != nil {
				return "", err
			}
			if err = os.Remove(filepath.Join(root, "config/bootstrap.sql")); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
		return strings.TrimSpace(string(out)), nil
	case "redis":
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)))
		if err != nil {
			return "", errors.New("SERVICE_PROTOCOL_UNREACHABLE")
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		err = probeRedis(conn, Binding{CredentialID: i.Config.CredentialID}, secret)
		conn.Close()
		if err != nil {
			return "", errors.New("SERVICE_AUTHENTICATION_FAILED")
		}
	case "nginx":
		body, err := serviceHTTP(ctx, "http://"+net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort))+"/__sfops_health", nil)
		if err != nil || string(body) != i.SoftwareID {
			return "", errors.New("SERVICE_SITE_HEALTH_FAILED")
		}
		if _, err = softwareCommand(ctx, x.softwareRunner, binary, "-p", root+"/", "-c", filepath.Join(root, "config/nginx.conf"), "-t"); err != nil {
			return "", errors.New("SERVICE_CONFIG_TEST_FAILED")
		}
		args = []string{"-v"}
	case "seaweedfs":
		if err = serviceS3Authentication(ctx, net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)), access, secret); err != nil {
			return "", err
		}
		args = []string{"version"}
	case "mediamtx":
		if err = checkMediaMTX(ctx, *i, secret); err != nil {
			return "", err
		}
	case "zlm":
		if err = validateNativeCertificate(filepath.Join(root, "runtime/dtls.pem")); err != nil {
			return "", err
		}
		if err = checkZLM(ctx, *i, secret); err != nil {
			return "", err
		}
	}
	output, err := softwareCommand(ctx, x.softwareRunner, binary, args...)
	version := strings.TrimSpace(string(output))
	if err != nil || version == "" || len(version) > 8192 || !nativeServiceVersionMatches(i.Definition, version) {
		return "", errors.New("SERVICE_VERSION_MISMATCH")
	}
	return version, nil
}

func nativeServiceVersionMatches(d serviceinstall.Definition, observed string) bool {
	if d.Key == "zlm" {
		return true
	}
	version := d.Version
	if d.Native != nil && d.Native.BuildRecipe == "DEB" {
		version = strings.SplitN(version, "-", 2)[0]
	}
	return strings.Contains(observed, version)
}
