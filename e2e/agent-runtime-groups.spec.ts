/**
 * E2E smoke test — runtime groups + priority override
 *
 * Verifies that:
 *  1. An agent assigned via a runtime group distributes tasks across the
 *     group's member runtimes.
 *  2. A priority override pins all subsequent tasks to a single runtime.
 *  3. Clearing the override restores normal distribution.
 */

import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Agent runtime groups + override", () => {
  let api: TestApiClient;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();
    await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) {
      await api.cleanup();
    }
  });

  test("agent uses group runtimes and respects priority override", async () => {
    // Create two online runtimes directly in the DB.
    const rt1 = await api.createRuntime({ status: "online", name: "E2E Group Runtime A" });
    const rt2 = await api.createRuntime({ status: "online", name: "E2E Group Runtime B" });

    // Create a runtime group containing both runtimes.
    const group = await api.createRuntimeGroup({
      name: "e2e-team-" + Date.now(),
      runtime_ids: [rt1.id, rt2.id],
    });

    // Create an agent assigned via group only (no direct runtime_ids).
    const agent = await api.createAgent({ group_ids: [group.id] });

    // --- Phase 1: normal distribution ---
    // 4 issues → expect tasks spread across both runtimes.
    const phase1Tasks: { runtime_id: string }[] = [];
    for (let i = 0; i < 4; i++) {
      const issue = await api.createIssue(`E2E Group Issue ${i + 1} ${Date.now()}`, {
        assignee_id: agent.id,
        assignee_type: "agent",
      });
      phase1Tasks.push(await api.getLatestTaskForIssue(issue.id));
    }
    const distinctBefore = new Set(phase1Tasks.map((t) => t.runtime_id)).size;
    expect(distinctBefore).toBeGreaterThanOrEqual(2);

    // --- Phase 2: override pins to rt1 ---
    await api.setRuntimeGroupOverride(group.id, rt1.id, new Date(Date.now() + 3_600_000));
    const phase2Tasks: { runtime_id: string }[] = [];
    for (let i = 0; i < 2; i++) {
      const issue = await api.createIssue(`E2E Group Override Issue ${i + 1} ${Date.now()}`, {
        assignee_id: agent.id,
        assignee_type: "agent",
      });
      phase2Tasks.push(await api.getLatestTaskForIssue(issue.id));
    }
    for (const t of phase2Tasks) {
      expect(t.runtime_id).toBe(rt1.id);
    }

    // --- Phase 3: clear override → distribution resumes ---
    await api.clearRuntimeGroupOverride(group.id);
    const phase3Tasks: { runtime_id: string }[] = [];
    for (let i = 0; i < 4; i++) {
      const issue = await api.createIssue(`E2E Group Post-Clear Issue ${i + 1} ${Date.now()}`, {
        assignee_id: agent.id,
        assignee_type: "agent",
      });
      phase3Tasks.push(await api.getLatestTaskForIssue(issue.id));
    }
    const distinctAfter = new Set(phase3Tasks.map((t) => t.runtime_id)).size;
    expect(distinctAfter).toBeGreaterThanOrEqual(2);
  });
});
