package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spfuzzylink/fabricchange"
)

var buildVersion = "dev"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		if _, err := fmt.Fprintln(out, "fabricchange", buildVersion); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: fabricchange plan -input FILE [-at RFC3339] [-format json|text]\n       fabricchange import-slurm -input CAPTURE")
		return 2
	}
	switch args[0] {
	case "plan", "import-slurm":
	default:
		fmt.Fprintln(errOut, "unknown command:", args[0])
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	input := fs.String("input", "", "input file, or - for stdin")
	at := fs.String("at", "", "evaluation time; defaults to current UTC (plan only)")
	format := fs.String("format", "json", "json or text (plan only)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *input == "" || fs.NArg() != 0 {
		fmt.Fprintln(errOut, "-input is required and positional arguments are not accepted")
		return 2
	}
	var reader io.Reader = os.Stdin
	if *input != "-" {
		f, err := os.Open(*input)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		defer f.Close()
		reader = f
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if args[0] == "import-slurm" {
		if *at != "" || *format != "json" {
			fmt.Fprintln(errOut, "import-slurm emits JSON and does not accept evaluation options")
			return 2
		}
		capture, err := fabricchange.ParseSlurmCapture(reader)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		if err := encoder.Encode(capture); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		return 0
	}
	if *format != "json" && *format != "text" {
		fmt.Fprintln(errOut, "-format must be json or text")
		return 2
	}
	now := time.Now().UTC()
	if *at != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		now = t
	}
	in, err := fabricchange.Decode(reader)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	report, err := fabricchange.Evaluate(in, now)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	if *format == "json" {
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	} else {
		if err := writeText(out, report); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	return fabricchange.ExitCode(report.Verdict)
}

func writeText(out io.Writer, r fabricchange.Report) error {
	// Buffer once so a broken output pipe is reported rather than ignored.
	var b []byte
	b = fmt.Appendf(b, "FabricChange: %s\nSnapshot: %s; evaluated: %s\n", r.Verdict, r.SnapshotAt.Format(time.RFC3339), r.EvaluatedAt.Format(time.RFC3339))
	for _, a := range r.Assumptions {
		b = fmt.Appendf(b, "ASSUMPTION: %s\n", a)
	}
	for _, f := range r.SnapshotFindings {
		b = fmt.Appendf(b, "UNKNOWN: %s\n", f.Message)
	}
	for _, w := range r.Waves {
		b = fmt.Appendf(b, "\nWave %s: %s; targets %v\n", w.ID, w.Verdict, w.Targets)
		for _, f := range w.Findings {
			b = fmt.Appendf(b, "  %s: %s\n", f.Verdict, f.Message)
		}
		for _, job := range w.Jobs {
			b = fmt.Appendf(b, "  Job %s owner=%s allocation=%s impact=%s baseline=%s resources=%v\n", job.ID, job.Owner, job.Status, job.State, job.BaselineState, job.Resources)
		}
		for _, impact := range w.Resources {
			b = fmt.Appendf(b, "  Resource %s: %s (baseline %s)\n", impact.ID, impact.State, impact.BaselineState)
			for _, c := range impact.Causes {
				b = fmt.Appendf(b, "    %v: %s", c.Path, c.Reason)
				if c.PathTruncated {
					b = fmt.Appendf(b, " [path truncated]")
				}
				b = append(b, '\n')
			}
			if impact.CausesTruncated {
				b = fmt.Appendf(b, "    Additional causes omitted; JSON and text cap cause detail at 16 per resource.\n")
			}
		}
		for _, c := range w.Capacity {
			b = fmt.Appendf(b, "  Capacity %s: %d healthy, %d unavailable, %d unknown\n", c.Domain, c.Healthy, c.Unavailable, c.Unknown)
		}
	}
	for _, l := range r.Limitations {
		b = fmt.Appendf(b, "LIMITATION: %s\n", l)
	}
	_, err := out.Write(b)
	return err
}
