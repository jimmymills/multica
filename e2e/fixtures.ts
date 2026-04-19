/**
 * TestApiClient — lightweight API helper for E2E test data setup/teardown.
 *
 * Uses raw fetch so E2E tests have zero build-time coupling to the web app.
 */

import "./env";
import pg from "pg";

// `||` (not `??`) so an empty `NEXT_PUBLIC_API_URL=` in .env still falls
// back to localhost. dotenv sets unset-vs-empty both as "" — treating them
// the same matches user intent.
const API_BASE = process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL = process.env.DATABASE_URL ?? "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

interface TestWorkspace {
  id: string;
  name: string;
  slug: string;
}

interface TestRuntime {
  id: string;
  name: string;
  status: string;
}

interface TestAgent {
  id: string;
  name: string;
}

interface TestRuntimeGroup {
  id: string;
  name: string;
}

interface TestTask {
  id: string;
  runtime_id: string;
  issue_id: string;
  status: string;
}

export class TestApiClient {
  private token: string | null = null;
  private workspaceSlug: string | null = null;
  private workspaceId: string | null = null;
  private createdIssueIds: string[] = [];
  private createdAgentIds: string[] = [];
  private createdRuntimeIds: string[] = [];
  private createdRuntimeGroupIds: string[] = [];

  async login(email: string, name: string) {
    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      // Keep each E2E login isolated so previous test runs do not trip the
      // per-email send-code rate limit.
      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      // Step 1: Send verification code
      const sendRes = await fetch(`${API_BASE}/auth/send-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!sendRes.ok) {
        throw new Error(`send-code failed: ${sendRes.status}`);
      }

      // Step 2: Read code from database
      const result = await client.query(
        "SELECT code FROM verification_code WHERE email = $1 AND used = FALSE AND expires_at > now() ORDER BY created_at DESC LIMIT 1",
        [email],
      );
      if (result.rows.length === 0) {
        throw new Error(`No verification code found for ${email}`);
      }

      // Step 3: Verify code to get JWT
      const verifyRes = await fetch(`${API_BASE}/auth/verify-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, code: result.rows[0].code }),
      });
      if (!verifyRes.ok) {
        throw new Error(`verify-code failed: ${verifyRes.status}`);
      }
      const data = await verifyRes.json();

      this.token = data.token;

      // Update user name if needed
      if (name && data.user?.name !== name) {
        await this.authedFetch("/api/me", {
          method: "PATCH",
          body: JSON.stringify({ name }),
        });
      }

      await client.query("DELETE FROM verification_code WHERE email = $1", [email]);

      return data;
    } finally {
      await client.end();
    }
  }

  async getWorkspaces(): Promise<TestWorkspace[]> {
    const res = await this.authedFetch("/api/workspaces");
    return res.json();
  }

  setWorkspaceId(id: string) {
    this.workspaceId = id;
  }

  setWorkspaceSlug(slug: string) {
    this.workspaceSlug = slug;
  }

  async ensureWorkspace(name = "E2E Workspace", slug = "e2e-workspace") {
    const workspaces = await this.getWorkspaces();
    const workspace = workspaces.find((item) => item.slug === slug) ?? workspaces[0];
    if (workspace) {
      this.workspaceId = workspace.id;
      this.workspaceSlug = workspace.slug;
      return workspace;
    }

    const res = await this.authedFetch("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ name, slug }),
    });
    if (res.ok) {
      const created = (await res.json()) as TestWorkspace;
      this.workspaceId = created.id;
      return created;
    }

    const refreshed = await this.getWorkspaces();
    const created = refreshed.find((item) => item.slug === slug) ?? refreshed[0];
    if (created) {
      this.workspaceId = created.id;
      return created;
    }

    throw new Error(`Failed to ensure workspace ${slug}: ${res.status} ${res.statusText}`);
  }

  /**
   * Insert a test agent_runtime row directly into the database.
   * Runtimes are normally registered by the daemon via heartbeat; there is no
   * user-facing REST endpoint to create them, so we use direct DB access here.
   * Each call generates a unique daemon_id so the UNIQUE (workspace_id, daemon_id, provider)
   * constraint is satisfied.
   */
  async createRuntime(opts: { status?: "online" | "offline"; name?: string } = {}): Promise<TestRuntime> {
    if (!this.workspaceId) throw new Error("workspaceId not set — call ensureWorkspace first");
    const status = opts.status ?? "online";
    const name = opts.name ?? `E2E Runtime ${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const daemonId = `e2e-daemon-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

    const client = new pg.Client(DATABASE_URL);
    await client.connect();
    try {
      const result = await client.query<TestRuntime>(
        `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
         VALUES ($1, $2, $3, 'local', 'claude', $4, 'E2E test device', '{}', now())
         RETURNING id, name, status`,
        [this.workspaceId, daemonId, name, status],
      );
      this.createdRuntimeIds.push(result.rows[0].id);
      return result.rows[0];
    } finally {
      await client.end();
    }
  }

  /**
   * Create an agent via the REST API with one or more runtime/group assignments.
   * Tracks the created agent for cleanup.
   */
  async createAgent(opts: { name?: string; runtime_ids?: string[]; group_ids?: string[] }): Promise<TestAgent> {
    const name = opts.name ?? `E2E Agent ${Date.now()}`;
    const res = await this.authedFetch("/api/agents", {
      method: "POST",
      body: JSON.stringify({
        name,
        runtime_ids: opts.runtime_ids ?? [],
        group_ids: opts.group_ids ?? [],
        visibility: "private",
        max_concurrent_tasks: 6,
      }),
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`createAgent failed: ${res.status} ${body}`);
    }
    const agent = (await res.json()) as TestAgent;
    this.createdAgentIds.push(agent.id);
    return agent;
  }

  /**
   * Create a runtime group via the REST API.
   * Tracks the created group for cleanup.
   */
  async createRuntimeGroup(opts: { name: string; runtime_ids: string[] }): Promise<TestRuntimeGroup> {
    const res = await this.authedFetch("/api/runtime-groups", {
      method: "POST",
      body: JSON.stringify(opts),
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`createRuntimeGroup failed: ${res.status} ${body}`);
    }
    const group = (await res.json()) as TestRuntimeGroup;
    this.createdRuntimeGroupIds.push(group.id);
    return group;
  }

  /**
   * Set a priority override on a runtime group so all tasks route to a single runtime.
   */
  async setRuntimeGroupOverride(groupId: string, runtimeId: string, endsAt: Date): Promise<void> {
    const res = await this.authedFetch(`/api/runtime-groups/${groupId}/override`, {
      method: "PUT",
      body: JSON.stringify({
        runtime_id: runtimeId,
        ends_at: endsAt.toISOString(),
      }),
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`setRuntimeGroupOverride failed: ${res.status} ${body}`);
    }
  }

  /**
   * Clear the priority override on a runtime group, resuming normal distribution.
   */
  async clearRuntimeGroupOverride(groupId: string): Promise<void> {
    const res = await this.authedFetch(`/api/runtime-groups/${groupId}/override`, {
      method: "DELETE",
    });
    if (!res.ok) {
      const body = await res.text();
      throw new Error(`clearRuntimeGroupOverride failed: ${res.status} ${body}`);
    }
  }

  /**
   * Fetch the most recent task queued/dispatched for an issue.
   * Uses GET /api/issues/{id}/task-runs which returns all tasks sorted newest-first.
   */
  async getLatestTaskForIssue(issueId: string): Promise<TestTask> {
    const res = await this.authedFetch(`/api/issues/${issueId}/task-runs`);
    if (!res.ok) {
      throw new Error(`getLatestTaskForIssue failed: ${res.status}`);
    }
    const tasks = (await res.json()) as TestTask[];
    if (tasks.length === 0) throw new Error(`No tasks found for issue ${issueId}`);
    return tasks[0];
  }

  async createIssue(title: string, opts?: Record<string, unknown>) {
    const res = await this.authedFetch("/api/issues", {
      method: "POST",
      body: JSON.stringify({ title, ...opts }),
    });
    const issue = await res.json();
    this.createdIssueIds.push(issue.id);
    return issue;
  }

  async deleteIssue(id: string) {
    await this.authedFetch(`/api/issues/${id}`, { method: "DELETE" });
  }

  /** Clean up all issues, agents, and runtimes created during this test. */
  async cleanup() {
    for (const id of this.createdIssueIds) {
      try {
        await this.deleteIssue(id);
      } catch {
        /* ignore — may already be deleted */
      }
    }
    this.createdIssueIds = [];

    for (const id of this.createdAgentIds) {
      try {
        await this.authedFetch(`/api/agents/${id}/archive`, { method: "POST" });
      } catch {
        /* ignore */
      }
    }
    this.createdAgentIds = [];

    if (this.createdRuntimeIds.length > 0 || this.createdRuntimeGroupIds.length > 0) {
      const client = new pg.Client(DATABASE_URL);
      await client.connect();
      try {
        for (const id of this.createdRuntimeGroupIds) {
          try {
            await client.query("DELETE FROM runtime_group WHERE id = $1", [id]);
          } catch {
            /* ignore */
          }
        }
        for (const id of this.createdRuntimeIds) {
          try {
            await client.query("DELETE FROM agent_runtime WHERE id = $1", [id]);
          } catch {
            /* ignore */
          }
        }
      } finally {
        await client.end();
      }
      this.createdRuntimeIds = [];
      this.createdRuntimeGroupIds = [];
    }
  }

  getToken() {
    return this.token;
  }

  private async authedFetch(path: string, init?: RequestInit) {
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...((init?.headers as Record<string, string>) ?? {}),
    };
    if (this.token) headers["Authorization"] = `Bearer ${this.token}`;
    if (this.workspaceSlug) headers["X-Workspace-Slug"] = this.workspaceSlug;
    else if (this.workspaceId) headers["X-Workspace-ID"] = this.workspaceId;
    return fetch(`${API_BASE}${path}`, { ...init, headers });
  }
}
