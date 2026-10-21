package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	args := []string{"container", "create", "--pull", "never", "--name", serviceName(i.SoftwareID), "--restart", "unless-stopped", "--security-opt", "no-new-privileges:true", "--memory", strconv.Itoa(c.MemoryMiB) + "m", "--cpus", "2", "--pids-limit", "512", "--log-driver", "json-file", "--log-opt", "max-size=10m", "--log-opt", "max-file=3"}
	for _, mapping := range serviceinstall.PortMappings(i.Definition, c) {
		args = append(args, "--publish", fmt.Sprintf("%s:%d:%d/%s", mapping.BindAddress, mapping.HostPort, mapping.ContainerPort, strings.ToLower(mapping.Protocol)))
	}
	labels := serviceLabels(x.installOwnership(req))
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--label", key+"="+labels[key])
	}
	mount := func(source, target string, readonly bool) {
		value := source + ":" + target
		if readonly {
			value += ":ro"
		}
		args = append(args, "--volume", value)
	}
	// The user/group is queried from the exact verified image, not guessed
	// from a moving Dockerfile or imposed uniformly across different services.
	gid := 0
	if i.Definition.Key == "mysql" || i.Definition.Key == "redis" || i.Definition.Key == "seaweedfs" {
		user := map[string]string{"mysql": "mysql", "redis": "redis", "seaweedfs": "seaweed"}[i.Definition.Key]
		out, runErr := x.serviceDocker(req, "SERVICE_CONFIGURE", 15*time.Second, "run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--entrypoint", "id", i.Definition.ImageID, "-g", user)
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
		if err = write("health.cnf", "[client]\nuser=root\npassword=\""+quote.Replace(secret)+"\"\nprotocol=socket\n", true); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "root-password"), "/run/secrets/mysql-root", true)
		mount(filepath.Join(config, "health.cnf"), "/run/secrets/mysql-health.cnf", true)
		mount(data, "/var/lib/mysql", false)
		args = append(args, "--env", "MYSQL_ROOT_PASSWORD_FILE=/run/secrets/mysql-root", "--env", "MYSQL_ROOT_HOST=localhost")
		if c.Database != "" {
			args = append(args, "--env", "MYSQL_DATABASE="+c.Database)
		}
		if c.Username != "" {
			if err = write("user-password", userSecret, true); err != nil {
				return nil, err
			}
			mount(filepath.Join(config, "user-password"), "/run/secrets/mysql-user", true)
			args = append(args, "--env", "MYSQL_USER="+c.Username, "--env", "MYSQL_PASSWORD_FILE=/run/secrets/mysql-user")
		}
		command = []string{"mysqld", "--mysqlx=0", "--port=3306", "--bind-address=0.0.0.0"}
	case "redis":
		hash := sha256.Sum256([]byte(secret))
		if err = write("users.acl", "user default on #"+hex.EncodeToString(hash[:])+" ~* &* +@all\n", true); err != nil {
			return nil, err
		}
		conf := fmt.Sprintf("bind 0.0.0.0\nport 6379\nprotected-mode yes\naclfile /run/secrets/redis-users.acl\ndir /data\nappendonly yes\nappendfsync everysec\nmaxmemory %dmb\nmaxmemory-policy noeviction\n", c.MemoryMiB*3/4)
		if err = write("redis.conf", conf, false); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "users.acl"), "/run/secrets/redis-users.acl", true)
		mount(filepath.Join(config, "redis.conf"), "/usr/local/etc/redis/redis.conf", true)
		mount(data, "/data", false)
		command = []string{"redis-server", "/usr/local/etc/redis/redis.conf"}
	case "nginx":
		conf := "events {}\nhttp { server { listen 80; server_name _; root /usr/share/nginx/html; location = /__sfops_health { default_type text/plain; return 200 \"" + i.SoftwareID + "\"; } location / { index index.html; } } }\n"
		if err = write("nginx.conf", conf, false); err != nil {
			return nil, err
		}
		if initialize {
			if err = serviceFile(filepath.Join(data, "index.html"), []byte("<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>Nginx</title><h1>Nginx is running</h1></html>"), 0, false); err != nil {
				return nil, err
			}
		}
		// A bind mount bypasses host-parent traversal, but the worker must be
		// able to traverse the mounted data directory itself.
		if err = os.Chmod(data, 0755); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "nginx.conf"), "/etc/nginx/nginx.conf", true)
		mount(data, "/usr/share/nginx/html", true)
		command = []string{"nginx", "-g", "daemon off;"}
	case "seaweedfs":
		identity := map[string]any{"identities": []any{map[string]any{"name": "ops-managed", "credentials": []any{map[string]string{"accessKey": accessKey, "secretKey": secret}}, "actions": []string{"Admin", "Read", "List", "Tagging", "Write"}}}}
		content, _ := json.Marshal(identity)
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
		mount(filepath.Join(config, "s3.json"), "/etc/seaweedfs/s3.json", true)
		mount(data, "/data", false)
		command = []string{"server", "-dir=/data/volume", "-master.dir=/data/master", "-volume.max=8", "-master.volumeSizeLimitMB=1024", "-ip=127.0.0.1", "-ip.bind=127.0.0.1", "-master.telemetry=false", "-volume.port=8081", "-filer", "-s3", "-s3.ip.bind=0.0.0.0", "-s3.port=8333", "-s3.port.iceberg=0", "-s3.port.lance=0", "-s3.config=/etc/seaweedfs/s3.json"}
	case "mediamtx":
		content := mediaMTXConfiguration(*i, secret, false)
		if err = write("mediamtx.yml", string(content), true); err != nil {
			return nil, err
		}
		if err = os.Chmod(data, 0755); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "mediamtx.yml"), "/mediamtx.yml", true)
		mount(data, "/data", false)
	case "zlm":
		if err = write("config.ini", zlmConfiguration(*i, secret, "/data", false), true); err != nil {
			return nil, err
		}
		mount(filepath.Join(config, "config.ini"), "/opt/media/conf/config.ini", true)
		mount(data, "/data", false)
		args = append(args, "--entrypoint", "/opt/media/bin/MediaServer")
		command = []string{"-c", "/opt/media/conf/config.ini", "-l", "1", "--log-dir", "/data/logs", "--log-size", "10", "--log-slice", "3", "-m", "3"}
	default:
		return nil, errors.New("SERVICE_RECIPE_UNSUPPORTED")
	}
	return append(append(args, i.Definition.ImageID), command...), nil
}
