package deploy

import (
	"reflect"
	"testing"
)

func TestCommandSourcePreservesArgvBoundaries(t *testing.T) {
	// Paths and registry/database values remain one argv entry, including shell
	// metacharacters and text which resembles another template placeholder.
	value := "/tmp/a b'$(touch ignored);${2}"
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"docker-stop", []string{value}, []string{"/usr/bin/docker", "container", "stop", "--time", "20", "--", value}},
		{"docker-mount", []string{value, "/data", ":ro"}, []string{"--volume", value + ":/data:ro"}},
		{"docker-mysql-database", []string{value}, []string{"--env", "MYSQL_DATABASE=" + value}},
		{"docker-apt", []string{value, "second argument"}, []string{"/usr/bin/apt-get", value, "second argument"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := deployCommand(test.name, test.args...); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("argv changed: %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCommandSourcesRejectMissingArguments(t *testing.T) {
	for _, name := range []string{"missing-source", "docker-stop", "docker-mount"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid built-in source must fail before executing a command")
				}
			}()
			deployCommand(name)
		})
	}
}
