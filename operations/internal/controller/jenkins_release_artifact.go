package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

const (
	jenkinsReleaseManifest = "sfops-release.txt"
	jenkinsReleaseLimit    = 512 << 20
)

var (
	jenkinsReleaseCommit          = regexp.MustCompile(`\A(?:[a-f0-9]{40}|[a-f0-9]{64})\z`)
	errJenkinsReleaseNotReady     = errors.New("JENKINS_RELEASE_NOT_READY")
	errJenkinsReleaseArtifact     = errors.New("JENKINS_RELEASE_ARTIFACT_INVALID")
	errJenkinsReleaseTooLarge     = errors.New("JENKINS_RELEASE_ARTIFACT_TOO_LARGE")
	errJenkinsReleaseMismatch     = errors.New("JENKINS_RELEASE_ARTIFACT_MISMATCH")
	errJenkinsReleaseNotSupported = errors.New("JENKINS_RELEASE_ARTIFACT_UNSUPPORTED")
)

type projectBuildArtifact struct {
	Commit   string
	Filename string
	SHA256   string
	Size     int64
}

func jenkinsReleaseOutput(kind string) (string, int64, bool) {
	switch kind {
	case "BACKEND":
		return "app.jar", deploy.JarLimit, true
	case "FRONTEND":
		return "web-dist.tar.gz", jenkinsReleaseLimit, true
	default:
		return "", 0, false
	}
}

// A release may read only the exact module-scoped job created by Ops.
// It cannot create or trigger a job, or follow external artifact URLs.
func (h *HTTP) fetchProjectReleaseArtifact(ctx context.Context, project applicationProject, module applicationModule, buildNumber int, dst io.Writer) (projectBuildArtifact, error) {
	if project.Archived {
		return projectBuildArtifact{}, errJenkinsArchived
	}
	if module.Catalog == nil {
		return projectBuildArtifact{}, errJenkinsReleaseNotSupported
	}
	filename, limit, ok := jenkinsReleaseOutput(module.Catalog.Kind)
	if !ok {
		return projectBuildArtifact{}, errJenkinsReleaseNotSupported
	}
	if !deploy.ID.MatchString(project.ID) || !deploy.ID.MatchString(module.ID) || buildNumber <= 0 || buildNumber > 1<<31-1 {
		return projectBuildArtifact{}, errJenkinsReleaseArtifact
	}
	select {
	case h.hostCheckSlots <- struct{}{}:
		defer func() { <-h.hostCheckSlots }()
	default:
		return projectBuildArtifact{}, errJenkinsBusy
	}
	budget := 20 * time.Second
	if dst != nil {
		budget = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	scope := jenkinsScope{project: project.ID, module: module.ID}
	monitor, endpoint, job, err := h.store.jenkinsBuildBinding(ctx, scope)
	if err != nil {
		return projectBuildArtifact{}, err
	}
	if monitor.automation == nil {
		return projectBuildArtifact{}, errJenkinsNotConfigured
	}
	token, err := monitor.openToken(h.store.Key, scope)
	if err != nil || !jenkinsToken.Match(token) {
		clear(token)
		return projectBuildArtifact{}, errJenkinsSecret
	}
	defer clear(token)
	client, done, err := h.store.connectJenkinsBuild(ctx, endpoint, *job, monitor.Username, token)
	if err != nil {
		return projectBuildArtifact{}, err
	}
	defer done()
	if err := client.inspectWorkflow(ctx, expectedJenkinsWorkflow(monitor.automation)); err != nil {
		return projectBuildArtifact{}, err
	}
	return client.releaseArtifact(ctx, buildNumber, filename, limit, dst)
}

func (c jenkinsBuildClient) releaseArtifact(ctx context.Context, number int, filename string, limit int64, dst io.Writer) (projectBuildArtifact, error) {
	prefix := strconv.Itoa(number) + "/"
	var run struct {
		Number    int    `json:"number"`
		Building  *bool  `json:"building"`
		Result    string `json:"result"`
		Artifacts []struct {
			FileName     string `json:"fileName"`
			RelativePath string `json:"relativePath"`
		} `json:"artifacts"`
	}
	if err := c.readJSON(ctx, prefix+"api/json", "number,building,result,artifacts[fileName,relativePath]", &run); err != nil {
		return projectBuildArtifact{}, err
	}
	if run.Number != number || run.Building == nil {
		return projectBuildArtifact{}, errJenkinsResponse
	}
	if *run.Building || run.Result != "SUCCESS" {
		return projectBuildArtifact{}, errJenkinsReleaseNotReady
	}
	seen := make(map[string]bool, 2)
	for _, item := range run.Artifacts {
		if item.FileName != item.RelativePath || (item.FileName != filename && item.FileName != jenkinsReleaseManifest) || seen[item.FileName] {
			return projectBuildArtifact{}, errJenkinsReleaseArtifact
		}
		seen[item.FileName] = true
	}
	if !seen[filename] || !seen[jenkinsReleaseManifest] {
		return projectBuildArtifact{}, errJenkinsReleaseArtifact
	}
	manifest, err := c.getWithVersionPolicy(ctx, c.jobPath(prefix+"artifact/"+jenkinsReleaseManifest, nil), true, true)
	if err != nil {
		return projectBuildArtifact{}, err
	}
	const maxManifest = 512
	data, err := io.ReadAll(io.LimitReader(manifest.Body, maxManifest+1))
	manifest.Body.Close()
	if err != nil || len(data) > maxManifest {
		return projectBuildArtifact{}, errJenkinsReleaseArtifact
	}
	fields := strings.Split(string(data), "\n")
	if len(fields) != 5 || fields[4] != "" || !jenkinsReleaseCommit.MatchString(fields[0]) || fields[1] != filename || !deploy.Hex.MatchString(fields[2]) {
		return projectBuildArtifact{}, errJenkinsReleaseArtifact
	}
	size, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || size < 1 || strconv.FormatInt(size, 10) != fields[3] {
		return projectBuildArtifact{}, errJenkinsReleaseArtifact
	}
	if size > limit {
		return projectBuildArtifact{}, errJenkinsReleaseTooLarge
	}
	result := projectBuildArtifact{Commit: fields[0], Filename: filename, SHA256: fields[2], Size: size}
	if dst == nil {
		return result, nil
	}
	res, err := c.getWithVersionPolicy(ctx, c.jobPath(prefix+"artifact/"+filename, nil), true, true)
	if err != nil {
		return projectBuildArtifact{}, err
	}
	defer res.Body.Close()
	if res.ContentLength >= 0 && res.ContentLength != size {
		return projectBuildArtifact{}, errJenkinsReleaseMismatch
	}
	hash := sha256.New()
	// Never write even one byte beyond the declared, bounded manifest length.
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(res.Body, size))
	if err != nil {
		return projectBuildArtifact{}, err
	}
	extra, err := io.Copy(io.Discard, io.LimitReader(res.Body, 1))
	if err != nil {
		return projectBuildArtifact{}, err
	}
	if n != size || extra != 0 || hex.EncodeToString(hash.Sum(nil)) != result.SHA256 {
		return projectBuildArtifact{}, errJenkinsReleaseMismatch
	}
	return result, nil
}
