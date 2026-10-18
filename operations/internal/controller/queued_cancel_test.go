package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

func TestQueuedCancellationFencesReleaseAndImportWorkers(t *testing.T) {
	s, b := setup(t)
	b.login()
	targetID := dataOf(t, b.call("POST", "/targets", map[string]any{"name": "cancel-target", "installRoot": "/opt/cancel-target"}, 201))["id"].(string)
	for _, kind := range []string{"PUBLISH", "ROLLBACK", "IMPORT"} {
		id := uuid.NewString()
		raw := jsonText(deploy.Request{TargetID: targetID, Candidate: &deploy.Release{ID: uuid.NewString()}})
		phase := "PREFLIGHT"
		if kind == "IMPORT" {
			raw = jsonText(importInput{Name: "jar", SHA: strings.Repeat("a", 64), Size: 1})
			phase = "VERIFY"
		}
		if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'QUEUED',?,?,'cancel-test',?,?)", id, b.currentAdmin(), kind, targetID, id, hashToken(id), phase, raw, Now(), Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+targetID, id); err != nil {
			t.Fatal(err)
		}
		b.call("POST", "/jobs/"+id+"/cancel", nil, 200)
		b.call("POST", "/jobs/"+id+"/cancel", nil, 200)
		if kind == "IMPORT" {
			b.h.processImport(id, raw)
		} else {
			b.h.processRelease(context.Background(), id, raw)
		}
		var status, gotPhase string
		var locks int
		if err := s.DB.QueryRow("SELECT status,phase FROM jobs WHERE id=?", id).Scan(&status, &gotPhase); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", id).Scan(&locks); err != nil {
			t.Fatal(err)
		}
		if status != "FAILED" || gotPhase != "CANCELLED" || locks != 0 {
			t.Fatal("cancelled worker resumed", kind, status, gotPhase, locks)
		}
	}
	for _, state := range []struct {
		status, phase string
		ack           int
	}{{"RUNNING", "TARGET_EXECUTION", 0}, {"UNKNOWN", "TARGET_EXECUTION", 0}, {"SUCCEEDED", "DONE", 1}, {"QUEUED", "UPLOADING", 0}} {
		id := uuid.NewString()
		if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,target_id,idempotency_key,request_hash,status,phase,input_json,target_ack_pending,trace_id,created_at,updated_at) VALUES(?,?,'IMPORT',?,?,?,?,?,'{}',?,'cancel-test',?,?)", id, b.currentAdmin(), targetID, id, hashToken(id), state.status, state.phase, state.ack, Now(), Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec("INSERT INTO operation_locks VALUES(?,?)", "target:"+targetID, id); err != nil {
			t.Fatal(err)
		}
		b.call("POST", "/jobs/"+id+"/cancel", nil, 409)
		var n int
		s.DB.QueryRow("SELECT count(*) FROM operation_locks WHERE job_id=?", id).Scan(&n)
		if n != 1 {
			t.Fatal("unsafe lock cleared")
		}
		s.DB.Exec("DELETE FROM operation_locks WHERE job_id=?", id)
	}
}

func TestCancelledImportRemovesOnlyItsUploadAndRetriesCleanup(t *testing.T) {
	s, b := setup(t)
	b.login()
	if err := os.MkdirAll(filepath.Join(s.Dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	otherUpload, artifact := s.uploadPath(uuid.NewString()), s.artifactPath(uuid.NewString())
	for _, name := range []string{otherUpload, artifact} {
		if err := os.WriteFile(name, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []string{"UPLOADED", "VERIFY"} {
		t.Run(phase, func(t *testing.T) {
			id := uuid.NewString()
			if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'IMPORT',?,?,'QUEUED',?,'{}','cancel-cleanup',?,?)", id, b.currentAdmin(), id, hashToken(id), phase, Now(), Now()); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.uploadPath(id), []byte("cancel this upload"), 0600); err != nil {
				t.Fatal(err)
			}
			b.call("POST", "/jobs/"+id+"/cancel", nil, 200)
			if _, err := os.Stat(s.uploadPath(id)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cancelled upload retained", err)
			}
			// A response-lost or interrupted cleanup retry remains safe and precise.
			if err := os.WriteFile(s.uploadPath(id), []byte("retry cleanup"), 0600); err != nil {
				t.Fatal(err)
			}
			b.call("POST", "/jobs/"+id+"/cancel", nil, 200)
			if _, err := os.Stat(s.uploadPath(id)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cleanup retry retained upload", err)
			}
			for _, name := range []string{otherUpload, artifact} {
				if body, err := os.ReadFile(name); err != nil || string(body) != "keep" {
					t.Fatal("cancel removed unrelated material", name, err)
				}
			}
		})
	}
	for _, state := range []struct{ status, phase string }{{"QUEUED", "UPLOADING"}, {"RUNNING", "VERIFY"}, {"UNKNOWN", "VERIFY"}} {
		id := uuid.NewString()
		if _, err := s.DB.Exec("INSERT INTO jobs(id,actor_id,kind,idempotency_key,request_hash,status,phase,input_json,trace_id,created_at,updated_at) VALUES(?,?,'IMPORT',?,?,?,?, '{}','cancel-cleanup',?,?)", id, b.currentAdmin(), id, hashToken(id), state.status, state.phase, Now(), Now()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.uploadPath(id), []byte("retain responsibility"), 0600); err != nil {
			t.Fatal(err)
		}
		b.call("POST", "/jobs/"+id+"/cancel", nil, 409)
		if _, err := os.Stat(s.uploadPath(id)); err != nil {
			t.Fatal("unsafe upload removed", state, err)
		}
	}
}
