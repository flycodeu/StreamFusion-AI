package deploy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestInstallResumeReusesArchivePrefixAndStopsAtCreateMarker(t *testing.T) {
	x, req := serviceRequestFixture(t)
	req.Installation.Source = serviceinstall.Source{Type: "ARCHIVE", ByteSize: 6, SHA256: strings.Repeat("a", 64)}
	req.InputHash = ""
	req.InputHash = Digest(req)
	if err := validateSoftwareInput(req); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	intent, _ := json.Marshal(req)
	for _, filename := range []string{filepath.Join(dir, "intent.json"), filepath.Join(x.root, "pending.json")} {
		if err := os.WriteFile(filename, intent, 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(dir, "service-image.partial")
	if err := receiveInstallArchive(archive, strings.NewReader("abc"), 6, 0); err == nil {
		t.Fatal("short transfer accepted as complete")
	}
	offset, err := x.installResumeOffset(req, dir)
	if err != nil || offset != 3 {
		t.Fatalf("offset=%d err=%v", offset, err)
	}
	if err = receiveInstallArchive(archive, strings.NewReader("def"), 6, offset); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(archive); err != nil || string(data) != "abcdef" {
		t.Fatalf("archive=%q err=%v", data, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "service-create.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = x.installResumeOffset(req, dir); err == nil {
		t.Fatal("container creation was eligible to replay")
	}
}

func TestNativeDownloadResumeAcceptsOnlyPartsBeforeMutation(t *testing.T) {
	for _, recipe := range [][2]string{{"java", "8"}, {"docker", "29.9.2-ubuntu24.04"}} {
		t.Run(recipe[0], func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			d, err := serviceinstall.Find(recipe[0], recipe[1])
			if err != nil {
				t.Fatal(err)
			}
			req.Installation.Definition = d
			req.Installation.Config = serviceinstall.Config{Name: recipe[0], RuntimeMode: "NATIVE", MemoryMiB: 512}
			req.Installation.Source = serviceinstall.Source{Type: "ONLINE"}
			req.InputHash = ""
			req.InputHash = Digest(req)
			if err = validateSoftwareInput(req); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(x.root, "jobs", req.JobID)
			cache := filepath.Join(dir, "native-download")
			if err = os.MkdirAll(cache, 0700); err != nil {
				t.Fatal(err)
			}
			intent, _ := json.Marshal(req)
			for _, file := range []string{filepath.Join(dir, "intent.json"), filepath.Join(x.root, "pending.json")} {
				if err = os.WriteFile(file, intent, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(cache, d.Native.Parts[0].Name), []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			if offset, err := x.installResumeOffset(req, dir); err != nil || offset != 0 {
				t.Fatal("target download should resume without controller upload offset", offset, err)
			}
			for _, marker := range []string{"native-owned.json", "docker-packages.json", "unknown-file"} {
				file := filepath.Join(dir, marker)
				if err = os.WriteFile(file, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err = x.installResumeOffset(req, dir); err == nil {
					t.Fatal("mutation evidence allowed replay:", marker)
				}
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(cache, "unexpected"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = x.installResumeOffset(req, dir); err == nil {
				t.Fatal("unexpected cache file allowed replay")
			}
		})
	}
}

func TestSoftwareStatusDoesNotDeclareNotStartedWithRemainingEvidence(t *testing.T) {
	for _, evidence := range []string{"official-update.json", "official-apply.json", "native-owned.json", "native-start.json", "docker-packages.json", "service-create.json", "service-start.json", "configuration.json", "configuration-result.json", "future-execution-marker.json", "material-directory"} {
		t.Run(evidence, func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			dir := filepath.Join(x.root, "jobs", req.JobID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, evidence)
			if evidence == "material-directory" {
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(file, []byte("remaining execution evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			req.Action = "STATUS"
			var out bytes.Buffer
			err := x.softwareLocked(req, &out)
			if err == nil || err.Error() != "TARGET_EVIDENCE_PRESENT_OR_DAMAGED" || out.Len() != 0 {
				t.Fatalf("remaining evidence was declared not started: err=%v output=%s", err, out.String())
			}
			for _, name := range []string{"intent.json", "result.json", "ack.json"} {
				if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("created %s while evidence was uncertain", name)
				}
			}
			if _, err := os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
				t.Fatal("created a new pending operation")
			}
		})
	}
}

func TestSoftwareStatusStillRecordsActuallyNotStartedAndAcknowledges(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("durable receipt directory fsync requires Linux")
	}
	for _, emptyDirectory := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "empty"}[emptyDirectory], func(t *testing.T) {
			x, req := serviceRequestFixture(t)
			if emptyDirectory {
				if err := os.MkdirAll(filepath.Join(x.root, "jobs", req.JobID), 0700); err != nil {
					t.Fatal(err)
				}
			}
			req.Action = "STATUS"
			var out bytes.Buffer
			if err := x.softwareLocked(req, &out); err != nil {
				t.Fatal(err)
			}
			var result Result
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != "FAILED" || result.Phase != "NOT_STARTED" || result.ErrorCode != "TARGET_NOT_STARTED" {
				t.Fatalf("unexpected result: %+v", result)
			}
			out.Reset()
			if err := x.softwareLocked(req, &out); err != nil {
				t.Fatal(err)
			}
			var repeated Result
			if err := json.Unmarshal(out.Bytes(), &repeated); err != nil || Digest(repeated) != Digest(result) {
				t.Fatal("status did not retain the original receipt", err)
			}
			req.Action, req.ResultHash = "ACK", Digest(result)
			out.Reset()
			if err := x.softwareLocked(req, &out); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
				t.Fatal("ack did not clear the exact pending operation")
			}
		})
	}
}

func TestSoftwareNotStartedRejectsJobDirectoryLink(t *testing.T) {
	x, req := serviceRequestFixture(t)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Skip("directory symlinks unavailable:", err)
	}
	if err := x.softwareNotStarted(req, dir, &bytes.Buffer{}); err == nil || err.Error() != "TARGET_EVIDENCE_PRESENT_OR_DAMAGED" {
		t.Fatalf("linked job directory accepted: %v", err)
	}
}

func TestSoftwareStatusKeepsExistingReceiptWithExecutionMarkers(t *testing.T) {
	x, req := serviceRequestFixture(t)
	dir := filepath.Join(x.root, "jobs", req.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	result := softwareResult(req)
	result.Status, result.Phase, result.ErrorCode = "FAILED", "OFFICIAL_PLAN_CHANGED", "OFFICIAL_PLAN_CHANGED"
	for name, value := range map[string]any{"intent.json": req, "result.json": result, "official-update.json": map[string]string{"JobID": req.JobID}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	req.Action = "STATUS"
	var out bytes.Buffer
	if err := x.softwareLocked(req, &out); err != nil {
		t.Fatal(err)
	}
	var observed Result
	if err := json.Unmarshal(out.Bytes(), &observed); err != nil || Digest(observed) != Digest(result) {
		t.Fatal("existing receipt changed", err)
	}
	if _, err := os.Lstat(filepath.Join(x.root, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("read-only status created a pending operation")
	}
}
