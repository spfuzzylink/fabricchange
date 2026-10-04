package fabricchange

import (
	"strings"
	"testing"
)

func TestSlurmCapture(t *testing.T) {
	text := "# started_at=2026-10-04T12:00:00Z\n101|alice|RUNNING|gpu01,gpu02\n102_3|bob|SUSPENDED|gpu03\n# finished_at=2026-10-04T12:00:02Z\n"
	c, err := ParseSlurmCapture(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Jobs) != 2 || c.Complete || len(c.Jobs[0].Resources) != 2 {
		t.Fatalf("unexpected capture %+v", c)
	}
	for _, text := range []string{
		"101|alice|RUNNING|gpu01\n",
		"# started_at=2026-10-04T12:00:00Z\n101|alice|RUNNING|gpu[01-02]\n# finished_at=2026-10-04T12:00:02Z\n",
		"# started_at=2026-10-04T12:00:00Z\n101|alice|RUNNING|gpu01\n",
		"# started_at=2026-10-04T12:00:00Z\n101|alice|PENDING|gpu01\n# finished_at=2026-10-04T12:00:02Z\n",
		"# started_at=2026-10-04T12:00:00Z\n101|alice|RUNNING|gpu01\n101|bob|RUNNING|gpu02\n# finished_at=2026-10-04T12:00:02Z\n",
	} {
		if _, err := ParseSlurmCapture(strings.NewReader(text)); err == nil {
			t.Fatalf("invalid/partial capture accepted: %s", text)
		}
	}
}
