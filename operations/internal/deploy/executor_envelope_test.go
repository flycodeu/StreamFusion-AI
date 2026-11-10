package deploy

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestRebindEnvelopeRoundTripPreservesBothDefinitionsAndPayload(t *testing.T) {
	_, req, _ := rebindRequestFixture(t)
	for n := range req.Management.Installation.Definition.Commands.Steps {
		req.Management.Installation.Definition.Commands.Steps[n].Script = strings.Repeat("x", 13000)
	}
	if err := req.Management.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(req)
	if err != nil || len(raw) <= 64<<10 || len(raw) >= 128<<10 {
		t.Fatal("fixture did not exercise the bounded larger envelope", len(raw), err)
	}
	input := bytes.NewBuffer(append(raw, '\n'))
	input.WriteString("remaining payload")
	parsed, remainder, err := ReadExecutorRequest(input)
	if err != nil || Digest(parsed) != Digest(req) {
		t.Fatal("valid double definition did not round trip", err)
	}
	payload, _ := io.ReadAll(remainder)
	if string(payload) != "remaining payload" {
		t.Fatal("envelope consumed payload")
	}
}

func TestExecutorEnvelopeKeepsOrdinary64KiBAndAbsolute128KiBBounds(t *testing.T) {
	for _, size := range []int{65 << 10, 129 << 10} {
		raw, _ := json.Marshal(Request{Action: "CAPABILITIES", TraceID: strings.Repeat("x", size)})
		if _, _, err := ReadExecutorRequest(bytes.NewReader(append(raw, '\n'))); err == nil || err.Error() != "ENVELOPE_INVALID" {
			t.Fatal("oversized ordinary envelope accepted", size, err)
		}
	}
	_, req, _ := rebindRequestFixture(t)
	req.TraceID = strings.Repeat("x", 129<<10)
	raw, _ := json.Marshal(req)
	if _, _, err := ReadExecutorRequest(bytes.NewReader(append(raw, '\n'))); err == nil {
		t.Fatal("rebind exceeded absolute limit")
	}
	req.TraceID, req.Management.Installation = strings.Repeat("x", 65<<10), nil
	raw, _ = json.Marshal(req)
	if _, _, err := ReadExecutorRequest(bytes.NewReader(append(raw, '\n'))); err == nil {
		t.Fatal("incomplete rebind received the larger allowance")
	}
}
