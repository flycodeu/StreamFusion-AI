package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecutorCheckExpiryIsDistinctFromTransportTimeout(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, test := range []struct {
		name, stderr, code string
		err                error
	}{
		{"fixed target deadline", "DEADLINE_EXCEEDED\n", "EXECUTOR_CHECK_EXPIRED", exit},
		{"network deadline", "", "EXECUTOR_CHECK_TIMEOUT", context.DeadlineExceeded},
		{"mixed untrusted output", "DEADLINE_EXCEEDED\nprivate-output", "EXECUTOR_CALL_FAILED", exit},
		{"untrusted substring", "private-output DEADLINE_EXCEEDED", "EXECUTOR_CALL_FAILED", exit},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := classifyExecutorInvocation(test.err, test.stderr)
			code, message := installTargetError(err)
			if code != test.code || strings.Contains(message, "private-output") || strings.Contains(message, "DEADLINE_EXCEEDED") {
				t.Fatal(code, message)
			}
			if test.code == "EXECUTOR_CHECK_EXPIRED" && !strings.Contains(message, "系统时间") {
				t.Fatal("expiry explanation omitted clock check", message)
			}
		})
	}
}
