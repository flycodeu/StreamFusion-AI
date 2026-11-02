package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPackageInventoryBeyondGeneralCommandLimit(t *testing.T) {
	var out strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&out, "desktop-package-%04d:amd64\tii \t1.2.3-0ubuntu1\n", i)
	}
	if out.Len() <= 64<<10 {
		t.Fatal("fixture must exceed the old output limit")
	}
	x := &executor{softwareRun: func(time.Duration, string, ...string) ([]byte, error) {
		return []byte(out.String()), nil
	}}
	packages, err := x.installedDockerPackages(context.Background())
	if err != nil || len(packages) != 2000 || packages["desktop-package-1999"] != "1.2.3-0ubuntu1" {
		t.Fatal("healthy desktop inventory rejected or truncated", len(packages), err)
	}
}

func TestPackageInventoryDistinguishesIncompleteAndUnreadable(t *testing.T) {
	for _, tc := range []struct{ name, output, code string }{
		{"held installed", "docker-ce\thi \t5:29.9.2\n", ""},
		{"removed configuration", "docker-ce\trc \t5:29.9.2\n", ""},
		{"not installed", "docker-ce\tun \t\n", ""},
		{"unpacked", "docker-ce\tiU \t5:29.9.2\n", "DOCKER_PACKAGE_TRANSACTION_PENDING"},
		{"reinstall required", "docker-ce\tiiR\t5:29.9.2\n", "DOCKER_PACKAGE_TRANSACTION_PENDING"},
		{"malformed", "unexpected diagnostic output\n", "DOCKER_PACKAGE_STATE_INVALID"},
		{"bounded inventory", strings.Repeat(" ", packageInventoryLimit), "DOCKER_PACKAGE_STATE_TOO_LARGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packages, err := parseInstalledPackages([]byte(tc.output))
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "held installed" && packages["docker-ce"] == "" {
					t.Fatal("held runtime treated as absent")
				}
				if tc.name != "held installed" && packages["docker-ce"] != "" {
					t.Fatal("removed runtime treated as installed")
				}
				return
			}
			if err == nil || err.Error() != tc.code {
				t.Fatal(err, tc.code)
			}
			if got := officialPackageStateCode(err); got != strings.Replace(tc.code, "DOCKER_", "OFFICIAL_", 1) {
				t.Fatal(got)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	x := &executor{softwareRun: func(time.Duration, string, ...string) ([]byte, error) { return nil, errors.New("private detail") }}
	if _, err := x.installedDockerPackages(ctx); err == nil || err.Error() != "DOCKER_PACKAGE_STATE_TIMEOUT" {
		t.Fatal(err)
	}
	if _, err := x.installedDockerPackages(context.Background()); err == nil || err.Error() != "DOCKER_PACKAGE_STATE_UNAVAILABLE" {
		t.Fatal(err)
	}
	if !strings.Contains(packageStateMessage("OFFICIAL_PACKAGE_STATE_UNAVAILABLE"), "尚未确认") {
		t.Fatal("unreadable inventory must not be diagnosed as an interrupted transaction")
	}
}
