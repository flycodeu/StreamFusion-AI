package controller

import (
	"database/sql"
	"errors"
	"net/http"
)

type applicationReferenceQuery interface {
	QueryRow(string, ...any) *sql.Row
}

// Drafts and archived projects retain their references until explicitly edited.
// A configuration is useful only while its selected host/account/runtime exists.
func applicationProjectReference(q applicationReferenceQuery, kind, id string) (string, error) {
	var query string
	args := []any{id}
	switch kind {
	case "host":
		query = `SELECT p.name FROM application_projects p WHERE
		 EXISTS(SELECT 1 FROM application_modules m WHERE m.project_id=p.id AND m.build_host_id=?) OR
		 EXISTS(SELECT 1 FROM application_deployments d WHERE d.project_id=p.id AND d.host_id=?) LIMIT 1`
		args = append(args, id)
	case "git-account":
		query = `SELECT p.name FROM application_projects p JOIN application_repositories r ON r.project_id=p.id WHERE r.git_account_id=? LIMIT 1`
	case "software":
		query = `SELECT p.name FROM application_projects p WHERE
		 EXISTS(SELECT 1 FROM application_modules m WHERE m.project_id=p.id AND m.build_software_id=?) OR
		 EXISTS(SELECT 1 FROM application_deployments d WHERE d.project_id=p.id AND d.runtime_software_id=?) LIMIT 1`
		args = append(args, id)
	case "node-environment":
		query = `SELECT p.name FROM application_projects p WHERE
		 EXISTS(SELECT 1 FROM application_modules m JOIN software_instances s ON s.id=m.build_software_id WHERE m.project_id=p.id AND s.node_environment_id=?) OR
		 EXISTS(SELECT 1 FROM application_deployments d JOIN software_instances s ON s.id=d.runtime_software_id WHERE d.project_id=p.id AND s.node_environment_id=?) LIMIT 1`
		args = append(args, id)
	default:
		return "", errors.New("APPLICATION_REFERENCE_KIND_INVALID")
	}
	var name string
	err := q.QueryRow(query, args...).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

func (h *HTTP) applicationReferenceBlocked(w http.ResponseWriter, r *http.Request, q applicationReferenceQuery, kind, id string) bool {
	project, err := applicationProjectReference(q, kind, id)
	if err != nil {
		h.internal(w, r, err)
		return true
	}
	if project == "" {
		return false
	}
	h.fail(w, r, http.StatusConflict, "APPLICATION_REFERENCE_IN_USE", "项目“"+project+"”仍在使用，请先调整项目配置")
	return true
}
