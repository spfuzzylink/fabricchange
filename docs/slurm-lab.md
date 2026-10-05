# Disposable live Slurm integration lab

## Why

Fixture tests can verify the parser without proving that the capture script works with a real scheduler. Operators need to know whether Slurm's actual array IDs, allocation states, ownership, and node names survive capture and import. This lab tests that integration boundary on a disposable CPU-only machine.

## What it verifies

The `Live Slurm lab` GitHub Actions workflow installs Ubuntu 24.04's packaged MUNGE and Slurm controller, worker, and clients. It starts one real controller and one real worker on the runner, submits a two-element array limited to one running task, and waits for the batch payload to execute.

It then runs the existing `scripts/capture-slurm.sh` and the built `fabricchange import-slurm` command. The Go verifier checks the exact running array-task ID, runner owner, expanded single-node name, exclusion of the pending task, and `complete: false`. After cancelling the recorded array job, it requires an empty scheduler queue, exit of the payload process, and an empty imported job list that still has `complete: false`.

**Status: the first live run passed and was independently reviewed.** [Run 37263057800](https://github.com/spfuzzylink/fabricchange/actions/runs/37263057800) executed on 2026-10-05 at 04:20–04:21 UTC (October 4 in America/Los_Angeles). All workflow steps ran successfully, including both live test cases and the final cleanup check. Ordinary `go test ./...` skips the live test and cannot establish this validation. The published `v0.1.0-alpha.1` assets are unchanged.

The runner used Slurm `23.11.4-1.2ubuntu5`, MUNGE `0.5.15-4ubuntu0.1`, one CPU node, and no GPU. The reviewed branch commit was `f1af14d0e86044b1cee8b3cf2e061d1ab3db37f2`; GitHub tested PR merge commit `0252f8362221a9a28a8de69a2a67a1ed3acdc84f`, whose source tree matched that branch commit. See the [preserved output excerpt](evidence/slurm-23.11.4-run.txt) for package versions, queue observations, captured text, imported JSON and pass results. The historical JSON retains the pre-verification limitation message printed by that source revision.

## How it runs

Use the repository's **Actions → Live Slurm lab → Run workflow**, or open a pull request affecting the workflow, capture script, lab, or Go sources. The workflow records the commit, run URL, installed package versions, queue observations, captures, and imported JSON in its log. The lab prints daemon diagnostics on failure without printing the MUNGE key.

The workflow runs this command on a GitHub-hosted Ubuntu 24.04 runner after installing the packages and Go:

```bash
timeout --signal=TERM --kill-after=75s 360s bash scripts/slurm-lab.sh --disposable-github-runner
```

This is an integration-test harness, **not an installation guide or production command**. It refuses local and self-hosted execution, root invocation, preexisting Slurm/MUNGE processes, and a preexisting `slurmstepd.scope`. The explicit command-line flag acknowledges that the host is disposable. Do not bypass these guards.

Configuration, credentials, state, and outputs live in a unique temporary directory. The controller uses a new state directory; a private MUNGE key/socket prevents authenticating to an existing cluster. The worker runs in a dedicated systemd service with delegation. Cleanup cancels only the recorded test array, stops the lab's services and Slurm-created scope, and deletes the lab directory. Package installation temporarily suppresses automatic service startup and restores that policy afterward. Polls and the overall workflow have time limits.

The setup follows the [Slurm administration quickstart](https://slurm.schedmd.com/quickstart_admin.html) and [slurmd documentation](https://slurm.schedmd.com/slurmd.html). Ubuntu 24.04's Slurm 23.11 packaging is the initial target; actual installed versions are recorded each run. Worker service settings follow the [Slurm 23.11 service template](https://github.com/SchedMD/slurm/blob/slurm-23-11-4-1/etc/slurmd.service.in). `task/none` and `proctrack/linuxproc` avoid making task confinement a prerequisite; Slurm may still initialize its cgroup subsystem, so the lab uses the runner's real systemd environment. It does not disable cgroups with unsupported configuration.

## What a pass cannot establish

This is one CPU node, one user, and one running array task. It does not exercise GPU hardware, multi-node hostlist expansion, suspended or completing allocations, federation, restricted-user visibility, topology collection, concurrent scheduler changes, production scale, or a real maintenance event. Array throttling creates a pending task to test exclusion; the planner does not predict pending-job placement. Slurm allocation capture remains incomplete inventory even when this lab passes.
