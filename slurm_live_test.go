package fabricchange

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestLiveSlurmLab consumes observations made by scripts/slurm-lab.sh only.
// Ordinary tests skip it: fixtures are not a substitute for real daemons/jobs.
func TestLiveSlurmLab(t *testing.T) {
	dir := os.Getenv("FABRICCHANGE_LIVE_SLURM_DIR")
	if dir == "" {
		t.Skip("requires scripts/slurm-lab.sh on a disposable GitHub-hosted runner")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
		t.Fatal("live lab requires a disposable GitHub-hosted runner")
	}
	wantBytes, err := os.ReadFile(filepath.Join(dir, "expected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSpace(string(wantBytes)), "\n")
	if len(want) != 4 || want[0] == want[1] {
		t.Fatalf("invalid live lab expectations: %q", want)
	}
	for _, name := range []string{"running", "empty"} {
		t.Run(name, func(t *testing.T) {
			input := filepath.Join(dir, name+".txt")
			out, err := exec.Command(filepath.Join(dir, "fabricchange"), "import-slurm", "-input", input).CombinedOutput()
			if err != nil {
				t.Fatalf("import-slurm failed: %v\n%s", err, out)
			}
			if err := os.WriteFile(filepath.Join(dir, name+".json"), out, 0600); err != nil {
				t.Fatal(err)
			}
			var got AllocationCapture
			decoder := json.NewDecoder(bytes.NewReader(out))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Complete || got.SchemaVersion != SchemaVersion || got.Source != "squeue-expanded-v1" || len(got.Limitations) == 0 {
				t.Fatalf("missing conservative capture metadata: %+v", got)
			}
			now := time.Now()
			if got.StartedAt.Before(now.Add(-10*time.Minute)) || got.FinishedAt.Before(got.StartedAt) || got.FinishedAt.After(now.Add(5*time.Second)) {
				t.Fatalf("invalid live capture timestamps: %+v", got)
			}
			expected := []Job{}
			if name == "running" {
				expected = append(expected, Job{ID: want[0], Owner: want[2], Status: "running", Resources: []string{want[3]}})
			}
			if !reflect.DeepEqual(got.Jobs, expected) {
				t.Fatalf("jobs = %+v, want %+v (pending array task %s must be absent)", got.Jobs, expected, want[1])
			}
		})
	}
}
