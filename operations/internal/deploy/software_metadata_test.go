package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSoftwareMetadataReportsPackageEvidenceWithoutRunningService(t *testing.T) {
	run := func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command != "/bin/sh" || len(args) != 4 || args[0] != "-c" || args[1] != softwareUnitMetadataScript || args[2] != "software-metadata" || args[3] != "mysql.service" {
			t.Fatal("unexpected metadata plan", command, args)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("metadata command has no deadline")
		}
		return []byte("FILE\tmysql.service\t/usr/lib/systemd/system/mysql.service\nEXE\tmysql.service\t/usr/sbin/mysqld\nPACKAGE\tmysql.service\tmysql-server-8.0\t8.0.43-0ubuntu0.24.04.1\n"), nil
	}
	actual := &SoftwareObservation{Kind: "SYSTEMD", Locator: "mysql.service", State: "RUNNING", IdentityHash: strings.Repeat("a", 64)}
	EnrichSoftwareObservation(context.Background(), actual, run)
	if actual.Version != "8.0.43-0ubuntu0.24.04.1" || actual.VersionSource != "PACKAGE" || actual.PackageName != "mysql-server-8.0" || actual.ExecutablePath != "/usr/sbin/mysqld" || actual.ServiceFile != "/usr/lib/systemd/system/mysql.service" {
		t.Fatal(actual)
	}
	if actual.State != "RUNNING" || actual.IdentityHash != strings.Repeat("a", 64) {
		t.Fatal("metadata changed lifecycle authority", actual)
	}
}

func TestSoftwareMetadataRejectsUnknownAndUnsafeEvidence(t *testing.T) {
	rows, err := readSoftwareUnitMetadata(context.Background(), []string{"mysql.service"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("FILE\tnginx.service\t/usr/lib/systemd/system/nginx.service\nPACKAGE\tnginx.service\tnginx\t1.26.1\nFILE\tmysql.service\t/tmp/mysql.service\nEXE\tmysql.service\t/proc/1/environ\nPACKAGE\tmysql.service\tmysql;id\t8.0\nPACKAGE\tmysql.service\tmysql\t8.0 password=secret\n"), nil
	})
	if err != nil || rows["mysql.service"].Version != "" || rows["mysql.service"].ExecutablePath != "" || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	for _, units := range [][]string{{"ssh.service"}, {"mysql.service;id"}, make([]string, 25)} {
		_, err := readSoftwareUnitMetadata(context.Background(), units, func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("invalid plan executed")
			return nil, nil
		})
		if err == nil {
			t.Fatal("unsafe metadata request accepted", units)
		}
	}
}

func TestSoftwareMetadataUnavailableKeepsHealthyState(t *testing.T) {
	actual := &SoftwareObservation{Kind: "SYSTEMD", Locator: "redis.service", State: "RUNNING", IdentityHash: "frozen"}
	EnrichSoftwareObservation(context.Background(), actual, func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("permission denied") })
	if actual.State != "RUNNING" || actual.IdentityHash != "frozen" || actual.Version != "" || actual.VersionStatus != "METADATA_UNAVAILABLE" {
		t.Fatal(actual)
	}
	EnrichSoftwareObservation(context.Background(), &SoftwareObservation{Kind: "DOCKER"}, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("container executed")
		return nil, nil
	})
}

func TestSoftwareMetadataPortsAreBoundedAndDoNotBorrowOtherProcesses(t *testing.T) {
	data := "FILE\tredis.service\t/lib/systemd/system/redis.service\nPORT\tother.service\t0.0.0.0:80/tcp\nPORT\tredis.service\t0.0.0.0:6379/tcp\nPORT\tredis.service\t0.0.0.0:6379/tcp\nPORT\tredis.service\t[::1]:6379/tcp\nPORT\tredis.service\t*:6380/tcp\nPORT\tredis.service\tpassword:6379/tcp\nPORT\tredis.service\t0.0.0.0:99999/tcp\n"
	rows, err := readSoftwareUnitMetadata(context.Background(), []string{"redis.service"}, func(context.Context, string, ...string) ([]byte, error) { return []byte(data), nil })
	if err != nil || len(rows["redis.service"].ListeningPorts) != 3 || rows["redis.service"].Version != "" {
		t.Fatal(rows, err)
	}
}

func TestSoftwareDiscoveryEnrichesNativeVersionsOnlyFromOwningPackage(t *testing.T) {
	report, err := DiscoverSoftware(context.Background(), func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "/bin/sh" && len(args) > 2 && args[2] == "software-metadata" {
			return []byte("FILE\tmysql.service\t/lib/systemd/system/mysql.service\nPACKAGE\tmysql.service\tmysql-server-8.0\t8.0.43\nFILE\tredis-server.service\t/lib/systemd/system/redis-server.service\nPACKAGE\tredis-server.service\tredis-server\t7:7.0.15-1\n"), nil
		}
		return discoveryFixture(command, args)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Candidates {
		if item.Locator == "mysql.service" && (item.Version != "8.0.43" || item.VersionSource != "PACKAGE") {
			t.Fatal(item)
		}
		if item.Locator == "nginx.service" && item.Version != "" {
			t.Fatal("version was borrowed from another unit", item)
		}
		if item.RuntimeKind == "DOCKER" && item.VersionSource != "IMAGE_REFERENCE" {
			t.Fatal("image tag was presented as a runtime version", item)
		}
	}
}
