package controller

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func annotationScenarioScript(t *testing.T) string {
	t.Helper()
	programs := [][3]string{}
	for _, app := range [][2]string{{"ytlabel", "NATIVE"}, {"label-studio", "NATIVE"}, {"label-studio", "DOCKER"}} {
		programs = append(programs, [3]string{app[0], app[1], annotationManagementProgram(app[0], app[1])})
	}
	operations := map[string]string{}
	for _, action := range []string{"read-config", "configure", "verify", "rollback", "check-data-migration", "migrate-data"} {
		operations[action] = annotationManagementSource(action + ".py")
	}
	raw, err := json.Marshal(map[string]any{"programs": programs, "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile("testdata/annotation-management-scenarios.py")
	if err != nil {
		t.Fatal(err)
	}
	return "import json\npayload=json.loads(" + strconv.Quote(string(raw)) + ")\n" + string(harness)
}

func TestAnnotationConfigurationFileScenarios(t *testing.T) {
	command := exec.Command(ytlabelTestPython(t), "-X", "utf8", "-I", "-S", "-")
	command.Stdin = bytes.NewBufferString(annotationScenarioScript(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated configuration scenarios: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

func TestAnnotationConfigurationLinuxTempScenarios(t *testing.T) {
	state, id := os.Getenv("SF_OPS_TEST_CONFIG20_STATE"), os.Getenv("SF_OPS_TEST_CONFIG20_TARGET")
	if state == "" || id == "" {
		t.Skip("set explicit read-only state and target for isolated /tmp Linux fixtures")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(state, "controller.sqlite"))+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := os.ReadFile(filepath.Join(state, "master.key"))
	if err != nil {
		t.Fatal("master key unavailable")
	}
	defer clear(key)
	store := &Store{DB: db, Dir: state, Key: key}
	host, err := store.readTarget(id)
	if err != nil {
		t.Fatal("test target unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, closeClient, err := store.connectSSH(ctx, host)
	if err != nil {
		t.Fatal(executorFailureCode(err))
	}
	defer closeClient()
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	out, failure := &limitedOutput{limit: 8192}, &limitedOutput{limit: 4096}
	err = runSSHCommand(client, "/usr/bin/python3 -I -S -B -c "+shellQuote(annotationScenarioScript(t)), nil, out, failure)
	if err != nil {
		t.Fatalf("isolated /tmp scenarios: %s\n%s", executorFailureCode(err), failure.Bytes())
	}
	t.Log(string(out.Bytes()))
}

func TestAnnotationConfigurationTemplateBounds(t *testing.T) {
	for _, d := range annotationSoftwareDefinitions() {
		raw, _ := json.Marshal(d)
		t.Logf("%s definition=%d prelude=%d", d.Key, len(raw), len(d.Management.ScriptPrelude))
		if err := d.ValidateCommands(); err != nil {
			t.Errorf("%s: %v", d.Key, err)
		}
	}
}
