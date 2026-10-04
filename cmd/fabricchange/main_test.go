package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIExitCodes(t *testing.T) {
	input := `{"schema_version":"1","snapshot":{"captured_at":"2026-10-04T12:00:00Z","complete":true,"resources":[{"id":"gpu01","kind":"compute","status":"up"}],"jobs":[]},"change":{"prior_wave_restored":true,"waves":[{"id":"one","targets":["gpu01"]}]},"policy":{"max_snapshot_age_seconds":300}}`
	for _, tc := range []struct {
		name, text string
		exit       int
	}{{"pass", input, 0}, {"unknown", strings.Replace(input, `"complete":true`, `"complete":false`, 1), 3}, {"blocked", strings.Replace(input, `"jobs":[]`, `"jobs":[{"id":"1","owner":"alice","status":"suspended","resources":["gpu01"]}]`, 1), 1}, {"invalid", `{"bad":true}`, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			code := run([]string{"plan", "-input", path, "-at", "2026-10-04T12:01:00Z"}, &out, &errOut)
			if code != tc.exit {
				t.Fatalf("exit %d want %d: %s", code, tc.exit, errOut.String())
			}
			if tc.exit != 2 && !strings.Contains(out.String(), "restored") {
				t.Fatal("restoration assumption absent from report")
			}
		})
	}
}
