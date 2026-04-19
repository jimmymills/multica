import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { CreateAgentDialog } from "./create-agent-dialog";
import type { RuntimeDevice, MemberWithUser, RuntimeGroup } from "@multica/core/types";

const runtime = (overrides: Partial<RuntimeDevice> = {}): RuntimeDevice => ({
  id: "rt-1",
  workspace_id: "ws-1",
  daemon_id: null,
  name: "Workstation",
  runtime_mode: "local",
  provider: "claude",
  launch_header: "",
  status: "online",
  device_info: "macOS",
  metadata: {},
  owner_id: "user-1",
  last_seen_at: null,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  ...overrides,
});

const members: MemberWithUser[] = [];

describe("CreateAgentDialog", () => {
  it("preselects the first filtered runtime", () => {
    render(
      <CreateAgentDialog
        runtimes={[runtime({ id: "rt-1", name: "Workstation" }), runtime({ id: "rt-2", name: "Laptop" })]}
        groups={[]}
        members={members}
        currentUserId="user-1"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    expect(screen.getByText("Workstation")).toBeInTheDocument();
    expect(screen.queryByText("Laptop")).not.toBeInTheDocument();
  });

  it("submits runtime_ids as an array of all selected ids", async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    render(
      <CreateAgentDialog
        runtimes={[
          runtime({ id: "rt-1", name: "Workstation" }),
          runtime({ id: "rt-2", name: "Laptop" }),
        ]}
        groups={[]}
        members={members}
        currentUserId="user-1"
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText(/deep research/i), { target: { value: "My Agent" } });
    fireEvent.click(screen.getByRole("button", { name: /add runtime/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /Laptop/i }));
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({
      name: "My Agent",
      runtime_ids: ["rt-1", "rt-2"],
    }));
  });

  it("disables Create when all runtimes are removed", () => {
    render(
      <CreateAgentDialog
        runtimes={[runtime({ id: "rt-1", name: "Workstation" })]}
        groups={[]}
        members={members}
        currentUserId="user-1"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText(/deep research/i), { target: { value: "My Agent" } });
    fireEvent.click(screen.getByLabelText("Remove Workstation"));
    expect(screen.getByText(/at least one runtime/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();
  });

  it("shows a distinct empty-state message when no runtimes exist", () => {
    render(
      <CreateAgentDialog
        runtimes={[]}
        groups={[]}
        members={members}
        currentUserId="user-1"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    expect(screen.getByText(/register a runtime/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();
  });

  it("submits group_ids when a group is selected", async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined);
    const g: RuntimeGroup = { id: "g1", workspace_id: "ws", name: "Team", description: "", runtimes: [], active_override: null, member_agent_count: 0, created_by: null, created_at: "", updated_at: "" };
    render(
      <CreateAgentDialog
        runtimes={[runtime({ id: "rt-1", name: "Workstation" })]}
        groups={[g]}
        members={[]}
        currentUserId="u1"
        onClose={vi.fn()}
        onCreate={onCreate}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText(/deep research/i), { target: { value: "A" } });
    fireEvent.click(screen.getByRole("button", { name: /add group/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /Team/i }));
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ group_ids: ["g1"] }));
  });

  it("enables Create when only a group is selected", () => {
    const g: RuntimeGroup = { id: "g1", workspace_id: "ws", name: "Team", description: "", runtimes: [], active_override: null, member_agent_count: 0, created_by: null, created_at: "", updated_at: "" };
    render(
      <CreateAgentDialog
        runtimes={[]}
        groups={[g]}
        members={[]}
        currentUserId="u1"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByPlaceholderText(/deep research/i), { target: { value: "A" } });
    fireEvent.click(screen.getByRole("button", { name: /add group/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /Team/i }));
    expect(screen.getByRole("button", { name: /^create$/i })).not.toBeDisabled();
  });
});
