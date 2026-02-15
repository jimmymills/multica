# SWE-bench: Agent Coding Benchmark

Run and evaluate the Multica agent against [SWE-bench](https://www.swebench.com/), the standard benchmark for AI coding agents. SWE-bench tasks are real GitHub issues from open-source Python projects — the agent must read the issue, explore the codebase, and produce a patch that fixes the bug.

## Quick Start

```bash
# 1. Download dataset (requires: pip install datasets)
python scripts/swe-bench/download-dataset.py --dataset lite --limit 5

# 2. Run the agent
npx tsx scripts/swe-bench/run.ts --limit 5

# 3. Analyze results
npx tsx scripts/swe-bench/analyze.ts
```

## Scripts

```
scripts/swe-bench/
├── download-dataset.py    # Download from HuggingFace → JSONL
├── run.ts                 # Core runner: Agent API → git diff → predictions
├── evaluate.sh            # Official Docker evaluation harness wrapper
├── analyze.ts             # Summarize run results
└── .gitignore             # Ignores downloaded datasets and output files
```

## Pipeline

```
                                    ┌──────────────────┐
  HuggingFace ──download──► JSONL ──┤  For each task:   │
                                    │  1. git clone     │
                                    │  2. git checkout   │
                                    │  3. Agent.run()   │
                                    │  4. git diff      │
                                    └────────┬─────────┘
                                             │
                              predictions.jsonl (SWE-bench format)
                                             │
                              ┌───────────────┴───────────────┐
                              │  swebench.harness (Docker)    │
                              │  Apply patch → run tests      │
                              │  → pass/fail verdict          │
                              └───────────────────────────────┘
```

## Dataset Variants

| Variant | Size | HuggingFace ID | Recommended For |
|---------|------|----------------|-----------------|
| **Lite** | 300 tasks | `princeton-nlp/SWE-bench_Lite` | Quick iteration, development |
| **Verified** | 500 tasks | `princeton-nlp/SWE-bench_Verified` | Official benchmarking, leaderboard |
| **Full** | ~2294 tasks | `princeton-nlp/SWE-bench` | Comprehensive evaluation |

```bash
# Download specific variant
python scripts/swe-bench/download-dataset.py --dataset verified
python scripts/swe-bench/download-dataset.py --dataset lite --limit 20
```

## Runner Options

```bash
npx tsx scripts/swe-bench/run.ts [options]

Options:
  --dataset PATH      JSONL dataset path          (default: scripts/swe-bench/lite.jsonl)
  --provider NAME     LLM provider                (default: kimi-coding)
  --model NAME        Model override
  --limit N           Max tasks to run             (default: all)
  --offset N          Skip first N tasks           (default: 0)
  --output PATH       Output predictions JSONL     (default: scripts/swe-bench/predictions.jsonl)
  --workdir PATH      Repo clone directory         (default: /tmp/swe-bench)
  --timeout MS        Per-task timeout             (default: 300000 = 5min)
  --instance ID       Run a single instance
  --debug             Enable debug logging
```

### Examples

```bash
# Run 10 tasks with Anthropic Claude
npx tsx scripts/swe-bench/run.ts --limit 10 --provider anthropic

# Run a specific instance
npx tsx scripts/swe-bench/run.ts --instance "django__django-16379"

# Resume from task 50 with longer timeout
npx tsx scripts/swe-bench/run.ts --offset 50 --limit 10 --timeout 600000

# Compare providers (run separately, different output files)
npx tsx scripts/swe-bench/run.ts --provider kimi-coding --output scripts/swe-bench/pred-kimi.jsonl
npx tsx scripts/swe-bench/run.ts --provider anthropic   --output scripts/swe-bench/pred-claude.jsonl
```

## How the Agent Solves Tasks

For each task, the runner:

1. **Clones the repository** to `/tmp/swe-bench/<instance_id>/` and checks out `base_commit`
2. **Creates an Agent** with a focused system prompt and restricted tools (coding only — no web, no cron, no sessions)
3. **Runs the agent** with the issue description as the prompt
4. **Collects `git diff`** as the patch after the agent finishes
5. **Appends** the prediction to `predictions.jsonl` in SWE-bench format

The agent has access to:
- `read`, `write`, `edit` — file operations
- `exec`, `process` — shell commands (for exploring code, running tests)
- `glob` — file search

Tools explicitly denied: `web_fetch`, `web_search`, `cron`, `data`, `sessions_spawn`, `sessions_list`, `memory_search`, `send_file`.

## Output Files

After a run, two files are produced:

### `predictions.jsonl` — SWE-bench format

```json
{"instance_id": "astropy__astropy-12907", "model_patch": "diff --git a/...", "model_name_or_path": "multica-kimi-coding"}
```

This file is the input to the official evaluation harness.

### `predictions.results.jsonl` — detailed run metrics

```json
{
  "instance_id": "astropy__astropy-12907",
  "success": true,
  "patch": "diff --git a/...",
  "error": null,
  "duration_ms": 141892,
  "session_id": "019c60c7-52ac-702a-9b9c-dc53c0daea6b"
}
```

## Analyzing Results

```bash
# Summary report
npx tsx scripts/swe-bench/analyze.ts

# Or specify a results file
npx tsx scripts/swe-bench/analyze.ts scripts/swe-bench/pred-kimi.results.jsonl
```

Output includes:
- Patch rate (how many tasks produced a diff)
- Duration statistics (avg/min/max)
- Error breakdown
- Per-repository stats
- Slowest tasks

### Run-Log Analysis

Each agent session writes a structured `run-log.jsonl` to `~/.super-multica/sessions/<session-id>/`. This captures every LLM call, tool invocation, and timing:

```bash
# Find a session's run log
cat ~/.super-multica/sessions/<session-id>/run-log.jsonl | head -5

# Quick stats from a run log
cat ~/.super-multica/sessions/<session-id>/run-log.jsonl | python3 -c "
import json, sys
events = [json.loads(l) for l in sys.stdin if l.strip()]
tools = [e for e in events if e['event'] == 'tool_start']
llm_ms = sum(e.get('duration_ms', 0) for e in events if e['event'] == 'llm_result')
print(f'LLM time: {llm_ms/1000:.1f}s | Tool calls: {len(tools)}')
"
```

## Official Evaluation (Docker)

The runner produces patches, but **only the official SWE-bench harness determines pass/fail** by applying the patch and running the project's test suite.

### Prerequisites

- Docker running (at least 120GB storage, 16GB RAM, 8 CPU cores)
- `pip install swebench`

### Run Evaluation

```bash
# Using the wrapper script
bash scripts/swe-bench/evaluate.sh

# Or directly
python -m swebench.harness.run_evaluation \
  --dataset_name princeton-nlp/SWE-bench_Lite \
  --predictions_path scripts/swe-bench/predictions.jsonl \
  --max_workers 4 \
  --run_id multica
```

Results are written to `logs/` and `evaluation_results/`.

## Known Limitations and Improvements

### Current Limitations

1. **No Docker isolation for agent execution**: The agent runs on the host, so `pip install` and other commands affect the system Python. SWE-bench standard practice is to run each task in a Docker container.

2. **`SMC_DATA_DIR` timing**: Setting `SMC_DATA_DIR` at runtime doesn't affect `DATA_DIR` (resolved at module import time). Sessions currently write to `~/.super-multica/sessions/`. To isolate, set the env var before the process starts:
   ```bash
   SMC_DATA_DIR=~/.swe-bench-eval npx tsx scripts/swe-bench/run.ts --limit 5
   ```

3. **Sequential execution**: Tasks run one at a time. For large-scale runs, launch multiple processes with `--offset`/`--limit` to parallelize:
   ```bash
   # Run 4 workers in parallel
   npx tsx scripts/swe-bench/run.ts --offset 0   --limit 75 --output pred-0.jsonl &
   npx tsx scripts/swe-bench/run.ts --offset 75  --limit 75 --output pred-1.jsonl &
   npx tsx scripts/swe-bench/run.ts --offset 150 --limit 75 --output pred-2.jsonl &
   npx tsx scripts/swe-bench/run.ts --offset 225 --limit 75 --output pred-3.jsonl &
   wait
   cat pred-*.jsonl > predictions.jsonl
   ```

4. **Repo cloning per instance**: Each instance clones the full repo. For repos with many tasks (e.g., astropy, django), a shared clone with `git worktree` would be faster.

### Potential Improvements

- **Docker-per-task**: Run each agent in a Docker container matching the SWE-bench environment spec (correct Python version, pre-installed dependencies)
- **Shared repo pool**: Clone each unique repo once, use `git worktree` for per-task isolation
- **Cost tracking**: Parse run-log token counts for per-task and aggregate cost estimates
- **Multi-turn retries**: If the agent produces no patch, retry with feedback
- **System prompt tuning**: The current prompt is minimal; more detailed guidance (e.g., "search for related test files to understand expected behavior") could improve solve rate

## Related Benchmarks

| Benchmark | Focus | Notes |
|-----------|-------|-------|
| [SWE-bench Verified](https://openai.com/index/introducing-swe-bench-verified/) | Bug fixing (Python) | Gold standard, 500 human-verified tasks |
| [SWE-bench Multilingual](https://github.com/SWE-bench/SWE-bench) | Bug fixing (7 languages) | Java, TS, JS, Go, Rust, C, C++ |
| [Terminal-Bench](https://www.swebench.com/) | CLI workflows | Multi-step sandboxed terminal tasks |
| [Aider Polyglot](https://aider.chat/docs/leaderboards/) | Code editing | 225 Exercism exercises, 6 languages |
| [DPAI Arena](https://www.jetbrains.com/) | Full dev workflow | JetBrains: patch, test, review, analysis |
| [HumanEval](https://github.com/openai/human-eval) | Function generation | 164 Python function tasks, largely saturated |

## Initial Results (kimi-coding, 3 tasks)

First run on 3 SWE-bench Lite tasks (all astropy):

| Task | Status | Duration | LLM Time | Tools | Fix |
|------|--------|----------|----------|-------|-----|
| `astropy__astropy-12907` | PATCHED | 141.9s | 125.1s | 30 | `_cstack`: `= 1` → `= right` |
| `astropy__astropy-14182` | PATCHED | 192.0s | 166.9s | 56 | Added `header_rows` param to RST writer |
| `astropy__astropy-14365` | PATCHED | 65.7s | 49.6s | 23 | `re.compile()` + `re.IGNORECASE` |

3/3 tasks produced patches. Formal evaluation pending (requires Docker harness).
