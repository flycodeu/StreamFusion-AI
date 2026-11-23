package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const JenkinsUnlockReadAction = "SOFTWARE_JENKINS_UNLOCK_READ"

// This response travels directly over the authenticated target connection. It
// deliberately does not use the replayable, persistent Result envelope.
type JenkinsUnlockReadResult struct {
	ControllerID    string `json:"controllerId"`
	TargetID        string `json:"targetId"`
	SoftwareID      string `json:"softwareId"`
	BindingRevision int64  `json:"bindingRevision"`
	IdentityHash    string `json:"identityHash"`
	Password        string `json:"password"`
}

var jenkinsBootstrapPassword = regexp.MustCompile(`^[0-9a-f]{32}$`)

var (
	jenkinsUnlockInvalid     = errors.New("JENKINS_UNLOCK_INVALID")
	jenkinsUnlockBinding     = errors.New("JENKINS_UNLOCK_BINDING_UNVERIFIED")
	jenkinsUnlockUnavailable = errors.New("JENKINS_UNLOCK_SETUP_UNAVAILABLE")
	jenkinsUnlockInactive    = errors.New("JENKINS_UNLOCK_SETUP_INACTIVE")
	jenkinsUnlockMissing     = errors.New("JENKINS_UNLOCK_SECRET_UNAVAILABLE")
)

// Only the four Ops Jenkins recipe kinds share the supported bootstrap layout.
func JenkinsUnlockRecipe(d serviceinstall.Definition) bool {
	if d.CatalogKey != "jenkins" || d.Kind != "SERVICE" || d.Commands == nil || d.Commands.Builtin {
		return false
	}
	switch d.Key {
	case "custom-jenkins-docker-online", "custom-jenkins-docker-offline":
		return d.Commands.Kind == "DOCKER"
	case "custom-jenkins-native-online", "custom-jenkins-native-offline":
		return d.Commands.Kind == "SYSTEMD"
	default:
		return false
	}
}

func (x *executor) readJenkinsUnlock(req Request, output io.Writer) error {
	allowed := Request{Action: JenkinsUnlockReadAction, ProtocolVersion: req.ProtocolVersion, ExecutorVersion: req.ExecutorVersion,
		ControllerID: req.ControllerID, TargetID: req.TargetID, TraceID: req.TraceID, DeadlineAt: req.DeadlineAt,
		CommandSignature: req.CommandSignature, Software: req.Software}
	s := req.Software
	if len(x.secrets) != 0 || !reflect.DeepEqual(req, allowed) || s == nil || s.Validate(false) != nil || s.Ownership == nil ||
		s.Ownership.ControllerID != req.ControllerID || s.Ownership.TargetID != req.TargetID || s.Ownership.CommandTemplateHash == "" {
		return jenkinsUnlockInvalid
	}
	if err := x.verifyCommandRequest(req); err != nil {
		return err
	}
	lock, err := jenkinsUnlockLock(x.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		return errors.New("TARGET_PENDING")
	}
	bindingFile := x.softwareFile(s.ID)
	var binding softwareBinding
	if secureAncestors(bindingFile) != nil || readJSON(bindingFile, &binding) != nil ||
		!reflect.DeepEqual(binding.Software, *s) || binding.JobID != s.Ownership.JobID {
		return jenkinsUnlockBinding
	}
	intentFile := filepath.Join(x.root, "jobs", binding.JobID, "intent.json")
	var intent Request
	if secureAncestors(intentFile) != nil || readJSON(intentFile, &intent) != nil ||
		intent.JobID != binding.JobID || intent.ControllerID != req.ControllerID || intent.TargetID != req.TargetID {
		return jenkinsUnlockBinding
	}
	i := frozenManagementInstallation(intent)
	if i == nil || !JenkinsUnlockRecipe(i.Definition) || i.Validate() != nil || i.SoftwareID != s.ID ||
		i.Definition.Commands.Kind != s.Kind || strings.ToUpper(i.Definition.Key) != s.SoftwareKey ||
		s.Ownership.ConfigHash != Digest(i.Config) || s.Ownership.CommandTemplateHash != Digest(i.Definition) || s.Ownership.RuntimeMode != i.Config.Mode() {
		return jenkinsUnlockBinding
	}
	root := serviceinstall.InstallRoot(x.root, s.ID, i.Config)
	if secureAncestors(root) != nil {
		return jenkinsUnlockBinding
	}
	bindAddress, port, err := jenkinsUnlockPort(*i, binding.Resources)
	if err != nil {
		return err
	}
	address, err := jenkinsLocalAddress(bindAddress)
	if err != nil {
		return err
	}
	deadline, _ := time.Parse(time.RFC3339, req.DeadlineAt)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 12*time.Second)
	defer timeout()
	if s.Kind == "DOCKER" {
		err = x.jenkinsDockerBinding(ctx, *s, root, bindAddress, port)
	} else {
		err = x.jenkinsNativeBinding(ctx, *s, root, bindAddress, port)
	}
	if err != nil {
		return err
	}
	endpoint := net.JoinHostPort(address, strconv.Itoa(port))
	if err = verifyJenkinsUnlockPage(ctx, endpoint, i.Definition.Version); err != nil {
		return err
	}
	password, err := readJenkinsBootstrapFile(root, s.ID)
	if err != nil {
		return err
	}
	// The wizard can finish concurrently in Jenkins without taking an Ops lock.
	if err = verifyJenkinsUnlockPage(ctx, endpoint, i.Definition.Version); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(JenkinsUnlockReadResult{
		ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: s.ID,
		BindingRevision: s.Version, IdentityHash: s.IdentityHash, Password: password,
	})
}

func jenkinsUnlockPort(i serviceinstall.Request, resources []serviceinstall.ResolvedResource) (string, int, error) {
	bindAddress, port := i.Config.BindAddress, i.Config.HostPort
	found := false
	for _, resource := range resources {
		if resource.Key != "main" || resource.Type != "PORT" {
			continue
		}
		if found || resource.Release || resource.Protocol != "TCP" {
			return "", 0, jenkinsUnlockBinding
		}
		bindAddress = resource.BindAddress
		port, _ = strconv.Atoi(resource.Value)
		found = true
	}
	if port < 1 || port > 65535 {
		return "", 0, jenkinsUnlockBinding
	}
	return bindAddress, port, nil
}

func jenkinsLocalAddress(raw string) (string, error) {
	address, err := netip.ParseAddr(raw)
	if err != nil || !address.Is4() || address.IsMulticast() || address.IsLinkLocalUnicast() {
		return "", jenkinsUnlockBinding
	}
	if address.IsUnspecified() {
		return "127.0.0.1", nil
	}
	if address.IsLoopback() {
		return raw, nil
	}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return "", jenkinsUnlockBinding
	}
	for _, candidate := range interfaces {
		p, err := netip.ParsePrefix(candidate.String())
		if err == nil && p.Addr() == address {
			return raw, nil
		}
	}
	return "", jenkinsUnlockBinding
}

func (x *executor) jenkinsDockerBinding(ctx context.Context, s SoftwareRequest, root, address string, port int) error {
	raw, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/docker", "container", "inspect", "--format",
		`{"id":{{json .Id}},"name":{{json .Name}},"image":{{json .Image}},"label":{{json (index .Config.Labels "streamfusion.ops.software-id")}},"running":{{json .State.Running}},"paused":{{json .State.Paused}},"mounts":{{json .Mounts}},"ports":{{json .NetworkSettings.Ports}}}`, "--", s.Locator)
	var actual struct {
		ID, Name, Image, Label string
		Running, Paused        bool
		Mounts                 []struct{ Type, Source, Destination string }
		Ports                  map[string][]struct{ HostIP, HostPort string }
	}
	if err != nil || json.Unmarshal(raw, &actual) != nil || actual.ID != s.Locator || actual.Name != "/sfops-"+s.ID ||
		actual.Image != s.Ownership.ImageID || actual.Label != s.ID || !actual.Running || actual.Paused ||
		Digest([]string{s.Kind, actual.ID, actual.Image}) != s.IdentityHash {
		return jenkinsUnlockBinding
	}
	cleanMount := false
	for _, mount := range actual.Mounts {
		switch {
		case mount.Destination == "/var/jenkins_home":
			if cleanMount || mount.Type != "bind" || mount.Source != filepath.Join(root, "data") {
				return jenkinsUnlockBinding
			}
			cleanMount = true
		case strings.HasPrefix(mount.Destination, "/var/jenkins_home/"):
			return jenkinsUnlockBinding
		}
	}
	for _, mapping := range actual.Ports["8080/tcp"] {
		if cleanMount && mapping.HostIP == address && mapping.HostPort == strconv.Itoa(port) {
			return nil
		}
	}
	return jenkinsUnlockBinding
}

func (x *executor) jenkinsNativeBinding(ctx context.Context, s SoftwareRequest, root, address string, port int) error {
	if s.Kind != "SYSTEMD" || s.Locator != "ops-app-"+s.ID+".service" || s.Ownership.BinaryPath != filepath.Join(root, "bin", "run") {
		return jenkinsUnlockBinding
	}
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State != "RUNNING" {
		return jenkinsUnlockBinding
	}
	pidOut, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", "--", s.Locator)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidOut)))
	if err != nil || pid < 2 {
		return jenkinsUnlockBinding
	}
	out, err := softwareCommand(ctx, x.softwareRunner, "/usr/bin/ss", "-H", "-lntp", "sport", "=", ":"+strconv.Itoa(port))
	if err != nil {
		return jenkinsUnlockBinding
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] != "LISTEN" || !strings.Contains(strings.Join(fields[5:], " "), ",pid="+strconv.Itoa(pid)+",") {
			continue
		}
		listener, listenerPort, err := net.SplitHostPort(fields[3])
		if err == nil && listenerPort == strconv.Itoa(port) {
			actual, _ := netip.ParseAddr(listener)
			if actual.Unmap().String() == address || address == "0.0.0.0" && (listener == "*" || actual.IsUnspecified()) {
				return nil
			}
		}
	}
	return jenkinsUnlockBinding
}

func verifyJenkinsUnlockPage(ctx context.Context, endpoint, version string) error {
	transport := &http.Transport{DisableKeepAlives: true, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint+"/login", nil)
	if err != nil {
		return jenkinsUnlockUnavailable
	}
	response, err := client.Do(request)
	if err != nil {
		return jenkinsUnlockUnavailable
	}
	defer response.Body.Close()
	media, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Jenkins") != version || version == "" || media != "text/html" {
		return jenkinsUnlockUnavailable
	}
	const maxLoginBytes = 256 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLoginBytes+1))
	if err != nil || len(body) > maxLoginBytes {
		return jenkinsUnlockUnavailable
	}
	// Upstream login.jelly includes this input only when isUsingSecurityToken
	// verifies both the incomplete setup state and the generated account.
	active, err := hasJenkinsUnlockInput(body)
	if err != nil {
		return jenkinsUnlockUnavailable
	}
	if !active {
		return jenkinsUnlockInactive
	}
	return nil
}

func hasJenkinsUnlockInput(body []byte) (bool, error) {
	// Jenkins renders the login form through Jelly. Read its markup as tokens so
	// attribute order, comments and strings inside other attributes do not matter.
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = false
	var inert []string
	for {
		token, err := decoder.RawToken()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Space != "" {
				continue
			}
			name := strings.ToLower(element.Name.Local)
			switch name {
			case "script", "style", "title", "textarea", "template", "noscript":
				if len(inert) == 0 || inert[len(inert)-1] == "template" {
					inert = append(inert, name)
				}
			case "input":
				if len(inert) == 0 && jenkinsUnlockAttributes(element.Attr) {
					return true, nil
				}
			}
		case xml.EndElement:
			if len(inert) != 0 && element.Name.Space == "" && strings.EqualFold(element.Name.Local, inert[len(inert)-1]) {
				inert = inert[:len(inert)-1]
			}
		}
	}
}

func jenkinsUnlockAttributes(attributes []xml.Attr) bool {
	values := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		if attribute.Name.Space != "" {
			continue
		}
		name := strings.ToLower(attribute.Name.Local)
		if _, duplicate := values[name]; duplicate {
			return false
		}
		values[name] = attribute.Value
	}
	return values["id"] == "security-token" && values["name"] == "j_password" && strings.EqualFold(values["type"], "password")
}
