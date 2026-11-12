package runtimepolicy

import "errors"
import "strconv"

const Version = 1

// Policy uses the native supervisor's options. Docker derives boot startup
// from its restart mode; systemd exposes it as a separate unit setting.
type Policy struct {
	Restart             string `json:"restart"`
	AutoStart           bool   `json:"autoStart"`
	RestartDelaySeconds int    `json:"restartDelaySeconds,omitempty"`
	StopTimeoutSeconds  int    `json:"stopTimeoutSeconds,omitempty"`
	MaxRetries          int    `json:"maxRetries,omitempty"`
}

type Change struct {
	Version int     `json:"version"`
	Policy  *Policy `json:"policy,omitempty"`
}

type State struct {
	Configured *Policy `json:"configured,omitempty"`
	Actual     *Policy `json:"actual,omitempty"`
	Held       bool    `json:"held"`
	ObservedAt string  `json:"observedAt"`
	Editable   bool    `json:"editable"`
	Reason     string  `json:"reason,omitempty"`
}

func (p Policy) DockerRestart() string {
	if p.Restart == "on-failure" && p.MaxRetries > 0 {
		return p.Restart + ":" + strconv.Itoa(p.MaxRetries)
	}
	return p.Restart
}

func (p Policy) Validate(kind string) error {
	if p.StopTimeoutSeconds < 1 || p.StopTimeoutSeconds > 600 || p.RestartDelaySeconds < 0 || p.RestartDelaySeconds > 300 || p.MaxRetries < 0 || p.MaxRetries > 100 {
		return errors.New("RUNTIME_POLICY_INVALID")
	}
	if p.Restart != "no" && p.Restart != "on-failure" && p.Restart != "always" && (kind != "DOCKER" || p.Restart != "unless-stopped") {
		return errors.New("RUNTIME_POLICY_UNSUPPORTED")
	}
	if kind == "SYSTEMD" && p.MaxRetries == 0 {
		return nil
	}
	if kind == "DOCKER" && p.RestartDelaySeconds == 0 && p.AutoStart == (p.Restart == "always" || p.Restart == "unless-stopped") && (p.MaxRetries == 0 || p.Restart == "on-failure") {
		return nil
	}
	return errors.New("RUNTIME_POLICY_UNSUPPORTED")
}
