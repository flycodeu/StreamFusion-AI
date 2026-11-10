package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func (x *executor) serviceSecrets(i serviceinstall.Request) (string, string, string, error) {
	c := i.Config
	secret := x.secrets[serviceinstall.SecretKey(c.CredentialID, c.CredentialRevision)]
	userSecret := x.secrets[serviceinstall.SecretKey(c.UserCredentialID, c.UserCredentialRevision)]
	accessKey := x.secrets[serviceinstall.SecretKey(c.AccessKeyCredentialID, c.AccessKeyCredentialRevision)]
	if err := serviceinstall.ValidateSecrets(i.Definition, c, x.secrets); err != nil {
		return "", "", "", err
	}
	return secret, userSecret, accessKey, nil
}

func serviceFile(filename string, content []byte, gid int, secret bool) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(content); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if secret {
		mode = 0640
	}
	if err = os.Chown(filename, 0, gid); err != nil {
		return err
	}
	return os.Chmod(filename, mode)
}

func (x *executor) prepareService(req Request, root string) ([]string, error) {
	return x.prepareServiceAt(req, root, filepath.Join(root, "config"), true)
}
func (x *executor) prepareServiceAt(req Request, root, outputConfig string, initialize bool) ([]string, error) {
	i, c := req.Installation, req.Installation.Config
	secret, userSecret, accessKey, err := x.serviceSecrets(*i)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(root)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return nil, err
	}
	if err = secureAncestors(parent); err != nil {
		return nil, err
	}
	if initialize {
		if err = os.Mkdir(root, 0700); err != nil {
			return nil, errors.New("SERVICE_DATA_DIRECTORY_EXISTS")
		}
	}
	config, data := filepath.Join(root, "config"), filepath.Join(root, "data")
	if initialize {
		for _, directory := range []string{config, data} {
			if err = os.Mkdir(directory, 0700); err != nil {
				return nil, err
			}
		}
	}
	args := deployCommand("docker-create-base", serviceName(i.SoftwareID), strconv.Itoa(c.MemoryMiB)+"m")[1:]
	for _, mapping := range serviceinstall.PortMappings(i.Definition, c) {
		args = append(args, deployCommand("docker-publish", fmt.Sprintf("%s:%d:%d/%s", mapping.BindAddress, mapping.HostPort, mapping.ContainerPort, strings.ToLower(mapping.Protocol)))...)
	}
	labels := serviceLabels(x.installOwnership(req))
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, deployCommand("docker-label", key+"="+labels[key])...)
	}
	mount := func(source, target string, readonly bool) {
		suffix := ""
		if readonly {
			suffix = ":ro"
		}
		args = append(args, deployCommand("docker-mount", source, target, suffix)...)
	}
	// The user/group is queried from the exact verified image, not guessed
	// from a moving Dockerfile or imposed uniformly across different services.
	gid := 0
	if i.Definition.Key == "mysql" || i.Definition.Key == "redis" || i.Definition.Key == "seaweedfs" {
		user := map[string]string{"mysql": "mysql", "redis": "redis", "seaweedfs": "seaweed"}[i.Definition.Key]
		out, runErr := x.serviceDockerSource(req, "SERVICE_CONFIGURE", 15*time.Second, "docker-user-group", i.Definition.ImageID, user)
		gid, err = strconv.Atoi(strings.TrimSpace(string(out)))
		if runErr != nil || err != nil || gid < 1 || gid > 65535 {
			return nil, errors.New("SERVICE_IMAGE_USER_UNAVAILABLE")
		}
	}
	write := func(name, text string, sensitive bool) error {
		return serviceFile(filepath.Join(outputConfig, name), []byte(text), gid, sensitive)
	}
	command := []string{}
	switch i.Definition.Key {
	case "mysql":
		if err = write("root-password", secret, true); err != nil {
			return nil, err
		}
		quote := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
		if err = write("health.cnf", deployTemplate("docker/mysql-health.cnf.tmpl", quote.Replace(secret)), true); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "root-password"), deployCommand("docker-mount-mysql-root")[0], true)
		mount(filepath.Join(config, "health.cnf"), deployCommand("docker-mount-mysql-health")[0], true)
		mount(data, deployCommand("docker-mount-mysql-data")[0], false)
		args = append(args, deployCommand("docker-mysql-root-env")...)
		if c.Database != "" {
			args = append(args, deployCommand("docker-mysql-database", c.Database)...)
		}
		if c.Username != "" {
			if err = write("user-password", userSecret, true); err != nil {
				return nil, err
			}
			mount(filepath.Join(config, "user-password"), deployCommand("docker-mount-mysql-user")[0], true)
			args = append(args, deployCommand("docker-mysql-user", c.Username)...)
		}
		command = deployCommand("docker-runtime-mysql")
	case "redis":
		hash := sha256.Sum256([]byte(secret))
		if err = write("users.acl", deployTemplate("docker/redis-users.acl.tmpl", hex.EncodeToString(hash[:])), true); err != nil {
			return nil, err
		}
		conf := deployTemplate("docker/redis.conf.tmpl", c.MemoryMiB*3/4)
		if err = write("redis.conf", conf, false); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "users.acl"), deployCommand("docker-mount-redis-users")[0], true)
		mount(filepath.Join(config, "redis.conf"), deployCommand("docker-mount-redis-config")[0], true)
		mount(data, deployCommand("docker-mount-data")[0], false)
		command = deployCommand("docker-runtime-redis")
	case "nginx":
		conf := deployTemplate("docker/nginx.conf.tmpl", i.SoftwareID)
		if err = write("nginx.conf", conf, false); err != nil {
			return nil, err
		}
		if initialize {
			if err = serviceFile(filepath.Join(data, "index.html"), []byte(deployScript("docker/nginx-index.html")), 0, false); err != nil {
				return nil, err
			}
		}
		// A bind mount bypasses host-parent traversal, but the worker must be
		// able to traverse the mounted data directory itself.
		if err = os.Chmod(data, 0755); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "nginx.conf"), deployCommand("docker-mount-nginx-config")[0], true)
		mount(data, deployCommand("docker-mount-nginx-data")[0], true)
		command = deployCommand("docker-runtime-nginx")
	case "seaweedfs":
		content := seaweedS3Configuration(accessKey, secret)
		if err = serviceFile(filepath.Join(outputConfig, "s3.json"), content, gid, true); err != nil {
			return nil, err
		}
		if initialize {
			for _, name := range []string{"master", "volume", "filerldb2"} {
				if err = os.Mkdir(filepath.Join(data, name), 0700); err != nil {
					return nil, err
				}
			}
		}
		mount(filepath.Join(config, "s3.json"), deployCommand("docker-mount-seaweedfs-config")[0], true)
		mount(data, deployCommand("docker-mount-data")[0], false)
		command = deployCommand("docker-runtime-seaweedfs")
	case "mediamtx":
		content := mediaMTXConfiguration(*i, secret, false)
		if err = write("mediamtx.yml", string(content), true); err != nil {
			return nil, err
		}
		if err = os.Chmod(data, 0755); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "mediamtx.yml"), deployCommand("docker-mount-mediamtx-config")[0], true)
		mount(data, deployCommand("docker-mount-data")[0], false)
	case "zlm":
		if err = write("config.ini", zlmConfiguration(*i, secret, deployCommand("docker-mount-data")[0], false), true); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "config.ini"), deployCommand("docker-mount-zlm-config")[0], true)
		mount(data, deployCommand("docker-mount-data")[0], false)
		args = append(args, deployCommand("docker-zlm-entrypoint")...)
		command = deployCommand("docker-runtime-zlm")
	default:
		return nil, errors.New("SERVICE_RECIPE_UNSUPPORTED")
	}
	return append(append(args, i.Definition.ImageID), command...), nil
}
