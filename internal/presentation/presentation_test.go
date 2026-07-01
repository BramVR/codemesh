package presentation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/commandresult"
)

func TestRenderJSONWritesCommandResult(t *testing.T) {
	result := commandresult.New("status", commandresult.ExitSuccess, commandresult.Diagnostics{}, map[string]string{"state": "present"})
	var stdout bytes.Buffer

	if err := RenderJSON(&stdout, result); err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Command   string            `json:"command"`
		ExitClass string            `json:"exit_class"`
		Payload   map[string]string `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "status" || payload.ExitClass != "success" || payload.Payload["state"] != "present" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRenderHumanUsesPayloadRenderer(t *testing.T) {
	result := commandresult.New("status", commandresult.ExitReadinessBlocked, commandresult.Diagnostics{}, "present")
	var stdout bytes.Buffer

	err := RenderHuman(&stdout, result, func(w io.Writer, payload string) error {
		_, err := fmt.Fprintf(w, "state: %s\n", payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "state: present\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "readiness-blocked") {
		t.Fatalf("human output leaked exit class: %q", stdout.String())
	}
}
