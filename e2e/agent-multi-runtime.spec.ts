/**
 * E2E smoke test — multi-runtime distribution
 *
 * Asserts that when an agent has two online runtimes assigned and two issues
 * are created with that agent as assignee, the resulting tasks are distributed
 * across both runtimes (i.e. each task carries a distinct runtime_id).
 *
 * Runtime creation goes directly to the DB because there is no user-facing
 * REST endpoint for it — runtimes are normally registered by the daemon via
 * heartbeat (POST /api/daemon/register).
 */

import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi } from "./helpers";
import type { TestApiClient } from "./fixtures";

test.describe("Agent multi-runtime distribution", () => {
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

  test("tasks distribute across multiple assigned runtimes", async () => {
    // Create two online runtimes directly in the DB (no user-facing API for this).
    const rt1 = await api.createRuntime({ status: "online", name: "E2E Runtime A" });
    const rt2 = await api.createRuntime({ status: "online", name: "E2E Runtime B" });

    // Create an agent assigned to both runtimes via the REST API.
    const agent = await api.createAgent({ runtime_ids: [rt1.id, rt2.id] });

    // Create two issues assigned to the agent.
    const issue1 = await api.createIssue("E2E Multi-Runtime Issue 1 " + Date.now(), {
      assignee_id: agent.id,
      assignee_type: "agent",
    });
    const issue2 = await api.createIssue("E2E Multi-Runtime Issue 2 " + Date.now(), {
      assignee_id: agent.id,
      assignee_type: "agent",
    });

    // Fetch the latest task enqueued for each issue.
    const task1 = await api.getLatestTaskForIssue(issue1.id);
    const task2 = await api.getLatestTaskForIssue(issue2.id);

    // The two tasks must have been routed to different runtimes.
    const distinctRuntimes = new Set([task1.runtime_id, task2.runtime_id]);
    expect(distinctRuntimes.size).toBe(2);

    // Both runtime IDs must be one of the two we created.
    expect([rt1.id, rt2.id]).toContain(task1.runtime_id);
    expect([rt1.id, rt2.id]).toContain(task2.runtime_id);
  });
});
