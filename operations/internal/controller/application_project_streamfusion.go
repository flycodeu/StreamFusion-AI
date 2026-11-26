package controller

import "path"

func streamFusionPlatformAPIDirectory(webDirectory string) string {
	dir := path.Clean(webDirectory)
	if path.Base(dir) != "platform-web" {
		return ""
	}
	return path.Join(path.Dir(dir), "platform-api")
}

// A StreamFusion UI is paired with the API beside it in the same repository.
// Module order breaks ties consistently between the project tree and Jenkins.
func streamFusionPlatformAPIModule(web applicationModule, modules []applicationModule) string {
	apiDirectory := streamFusionPlatformAPIDirectory(web.Directory)
	if apiDirectory == "" {
		return ""
	}
	for _, module := range modules {
		if module.RepositoryID == web.RepositoryID && path.Clean(module.Directory) == apiDirectory {
			return module.ID
		}
	}
	return ""
}

func readStreamFusionPlatformAPIModule(q applicationReader, projectID string, web applicationModule) (string, error) {
	if web.RepositoryID == "" || streamFusionPlatformAPIDirectory(web.Directory) == "" {
		return "", nil
	}
	rows, err := q.Query(`SELECT id,directory FROM application_modules WHERE project_id=? AND repository_id=?
		ORDER BY position,id`, projectID, web.RepositoryID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var modules []applicationModule
	for rows.Next() {
		module := applicationModule{RepositoryID: web.RepositoryID}
		if err := rows.Scan(&module.ID, &module.Directory); err != nil {
			return "", err
		}
		modules = append(modules, module)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return streamFusionPlatformAPIModule(web, modules), nil
}
