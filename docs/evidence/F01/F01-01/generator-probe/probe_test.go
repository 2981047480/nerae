package probe

import (
	"encoding/json"
	"testing"
)

func TestGeneratedProbeModel(t *testing.T) {
	input := Probe{Status: ProbeStatus("ready")}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"status":"ready"}` {
		t.Fatalf("unexpected generated model JSON: %s", body)
	}
}
