# TESTS.md — Tool Invocation Diagnostic Suite

Purpose
- Exercise every tool available to the agent as a diagnostic test suite.
- The model's objective: attempt to execute tests for EVERY tool and produce a single machine-readable JSON report summarizing pass/fail for every test.

How to run
- Non-interactive: coder -p "$(cat TESTS.md)"  (or pass file contents as the prompt).
- Interactive: paste the file contents into the agent's prompt or load as system/user context.

Instructions to the model (Test Runner role)

**CRITICAL**: You MUST execute ALL of the following tool tests sequentially, in order, before emitting the final JSON report. Do NOT skip tools, do NOT stop early, and do NOT emit the final report until you have attempted every single tool listed below.

1. Role and objective: You are a Test Runner in DIAGNOSTIC mode. Your task is to execute one focused test for EACH tool available to you. Your primary goal is to determine whether the tool works on this system and report the outcome truthfully. You will test tools in a strict sequence. You will not emit the final summary report until every tool has been tested.

2. Invocation method:
   - Prefer the platform's native tool-calling API (the environment may support structured function/tool calls). Use that when available.
   - If native tool-calling is not supported, use a single fenced JSON block as a fallback. That block must contain a single JSON object with two keys: "name" (the tool name string) and "arguments" (an object of parameters). Place that JSON object inside a markdown code fence labeled json. Emit exactly one such fenced block per intended invocation, with no additional commentary inside the fence.

3. Execution flow for each test:
   - Announce (in human-readable text) which tool you are going to test and why.
   - Attempt the invocation (native call or fallback fenced JSON) and wait for the tool result.
   - Inspect the returned result and determine whether the test passed or failed. Pass/fail criteria should be simple and deterministic (e.g., returned a non-empty listing, returned non-empty file contents, returned an exit code indicating success, created and read back an expected marker).
   - Record a short diagnostic message indicating success, failure, or error details. If an invocation errors or the tool is unavailable, mark its tests as failed and include the error text.
   - Move immediately to the next tool. Do not elaborate or add extra narrative between tools.

4. Determinism and style:
   - Use low randomness: behave as if temperature is 0 (deterministic responses).
   - Do not fabricate results. If you must guess, instead mark the test as failed and document the reason.
   - After each tool call, do not emit lengthy output. Wait for the tool result, then proceed to the next test immediately.
   - Do not produce the final summary until ALL tests are attempted. This is mandatory.

5. Sequential tool tests (execute in this exact order):
   1. list_directory - List the current directory (path: ".") and verify at least one entry is returned.
   2. read_file - Read package.json from the root and verify non-empty content.
   3. write_file - Create a temporary diagnostic marker file (e.g., "/tmp/coder_test_marker.txt" with content "DIAGNOSTIC_TEST_PASSED") and verify it was created.
   4. search_files - Search for "TODO" or "FIXME" comments in the workspace and report whether matches were found.
   5. execute_command - Run "git status" (or equivalent VCS command) and verify exit code indicates success.
   6. run_tests - Attempt to run project tests if available (e.g., "npm test" or "go test ./..."), or mark as unavailable if no test runner exists.
   7. read_git_log - Read the last 3 commits from git log and verify non-empty output.
   8. Any remaining tools - For each tool not listed above, perform a simple safe invocation and report pass/fail.

   RULE: After each tool test completes, immediately move to the next. Do not pause. Do not wait for user input. Execute all tests in sequence.

6. Failure handling:
   - If a tool is not registered or cannot be invoked, record that it was unavailable and mark its tests failed.
   - If permissions prevent a test, record the permission error and fail the test.
   - If a tool times out, record the timeout and mark as failed.
   - Continue to the next tool regardless of the outcome of the previous one.

Final report (machine-readable) — EMIT THIS AFTER ALL TESTS COMPLETE

**YOU MUST EMIT THE JSON REPORT BELOW AFTER EVERY SINGLE TOOL HAS BEEN TESTED. DO NOT SKIP THIS STEP.**

After all tests in section 5 have been attempted (regardless of pass/fail status), emit exactly one JSON object as the final output. Include no additional freeform text before or after this JSON.

The JSON schema to emit exactly (keys and types):
```json
{
  "summary": {
    "passed": <integer>,
    "failed": <integer>,
    "total": <integer>
  },
  "results": [
    {
      "test_id": "tools/<tool_name>/<n>",
      "tool": "<tool_name>",
      "arguments": { /* object exactly as called, or {} */ },
      "expected": "<brief expected condition>",
      "passed": true | false,
      "details": "<short diagnostic message or error>"
    }
  ]
}
```

Notes for running against non-cloud models
- Many locally-hosted code models (e.g., qwen2.5-coder:7b, qwen3-coder, gemma4) will not natively emit structured tool-call objects. In those cases the model should use the fenced-JSON fallback described above.
- Keep instructions explicit and deterministic to coax consistent JSON or native calls.

Integrator notes
- The CLI will parse native tool call objects when the provider emits them or fall back to the fenced-JSON format. The TESTS.md file is intended to be fed to the CLI in headless mode (-p) or used interactively.
- The final JSON must be printed exactly once and without extra text so automated tooling can consume it.

End of file
