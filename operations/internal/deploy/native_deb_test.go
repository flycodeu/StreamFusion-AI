package deploy

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func debPayloadFixture(t *testing.T, entries []*tar.Header, bodies []string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	for n, entry := range entries {
		if entry.Typeflag == tar.TypeReg {
			entry.Size = int64(len(bodies[n]))
		}
		if err := writer.WriteHeader(entry); err != nil {
			t.Fatal(err)
		}
		if entry.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(bodies[n])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDebPayloadSelectsOnlyFixedExecutables(t *testing.T) {
	root := t.TempDir()
	data := debPayloadFixture(t, []*tar.Header{
		{Name: "./", Typeflag: tar.TypeDir},
		{Name: "./usr/bin/redis-check-rdb", Typeflag: tar.TypeReg, Mode: 0755},
		{Name: "./etc/redis/redis.conf", Typeflag: tar.TypeReg, Mode: 0644},
		{Name: "./usr/bin/redis-server", Typeflag: tar.TypeSymlink, Linkname: "redis-check-rdb"},
	}, []string{"", "vendor executable bytes", "system configuration must stay untouched", ""})
	wanted := map[string]string{"usr/bin/redis-check-rdb": "runtime/redis/redis-server"}
	if err := extractDebPayloadTar(context.Background(), bytes.NewReader(data), root, wanted); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(root, "runtime/redis/redis-server")); err != nil || string(content) != "vendor executable bytes" {
		t.Fatal(string(content), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "etc")); !os.IsNotExist(err) {
		t.Fatal("unselected package system files extracted")
	}
	if err := extractDebPayloadTar(context.Background(), bytes.NewReader(data), root, wanted); err == nil {
		t.Fatal("existing executable overwritten")
	}
}

func TestDebPayloadRejectsMissingLinksTraversalAndCancellation(t *testing.T) {
	wanted := map[string]string{"usr/sbin/nginx": "bin/nginx"}
	for _, entry := range []*tar.Header{
		{Name: "./usr/sbin/nginx", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
		{Name: "./usr/sbin/nginx", Typeflag: tar.TypeLink, Linkname: "etc/passwd"},
		{Name: "../../escape", Typeflag: tar.TypeReg, Mode: 0755},
		{Name: "./unselected-file", Typeflag: tar.TypeReg, Mode: 0755},
	} {
		data := debPayloadFixture(t, []*tar.Header{entry}, []string{"x"})
		if extractDebPayloadTar(context.Background(), bytes.NewReader(data), t.TempDir(), wanted) == nil {
			t.Fatal("unsafe/incomplete payload accepted", entry)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if extractDebPayloadTar(ctx, bytes.NewReader(nil), t.TempDir(), wanted) == nil {
		t.Fatal("canceled payload extraction succeeded")
	}
	data := debPayloadFixture(t, []*tar.Header{{Name: "./usr/sbin/nginx", Typeflag: tar.TypeReg, Mode: 0755}}, []string{"x"})
	if extractDebPayloadTar(context.Background(), bytes.NewReader(data), t.TempDir(), map[string]string{"usr/sbin/nginx": "../escape"}) == nil {
		t.Fatal("unsafe fixed destination accepted")
	}
}

func TestDebPayloadMapsRealRedisBinaryAndPreservesArgv(t *testing.T) {
	d, _ := serviceinstall.Find("redis", "7.0.15-1ubuntu0.24.04.5")
	payloads := debProgramPayloads(d)
	if payloads["redis-tools_7.0.15-1ubuntu0.24.04.5_amd64.deb"]["usr/bin/redis-check-rdb"] != "runtime/redis/redis-server" || payloads["liblzf1_3.6-4_amd64.deb"]["usr/lib/x86_64-linux-gnu/liblzf.so.1.5"] != "runtime/lib/liblzf.so.1" {
		t.Fatal(payloads)
	}
	wrapper := redisDebWrapper("/srv/redis", "redis-server")
	for _, required := range []string{"--argv0 'redis-server'", "--library-path " + posixSingleQuote(filepath.Join("/srv/redis", "runtime/lib")), posixSingleQuote(filepath.Join("/srv/redis", "runtime/redis", "redis-server")), `"$@"`} {
		if !strings.Contains(wrapper, required) {
			t.Fatal("wrapper lost fixed argv/library path", wrapper)
		}
	}
	if strings.Contains(wrapper, "LD_LIBRARY_PATH=") || strings.Contains(wrapper, "apt") {
		t.Fatal("wrapper polluted the process environment or installs a package", wrapper)
	}
}

func TestNativeDistributionAndJava8VersionChecks(t *testing.T) {
	redis, _ := serviceinstall.Find("redis", "7.0.15-1ubuntu0.24.04.5")
	if !nativeServiceVersionMatches(redis, "Redis server v=7.0.15 sha=00000000 malloc=jemalloc") || nativeServiceVersionMatches(redis, "Redis server v=7.4.11") {
		t.Fatal("DEB release suffix confused the application version")
	}
	java, _ := serviceinstall.Find("java", "8")
	if !installedToolVersionMatches(java, "openjdk version \"1.8.0_504\"\nOpenJDK Runtime Environment (Temurin)(build 1.8.0_504-b01)") || installedToolVersionMatches(java, "openjdk version \"1.8.0_402\"\nOpenJDK Runtime Environment (Temurin)(build 1.8.0_402-b06)") {
		t.Fatal("Java8 exact pinned version check failed")
	}
}
