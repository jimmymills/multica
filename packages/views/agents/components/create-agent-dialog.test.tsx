import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { CreateAgentDialog } from "./create-agent-dialog";
import type { RuntimeDevice, MemberWithUser } from "@multica/core/types";

const runtime = (overrides: Partial<RuntimeDevice> = {}): RuntimeDevice => ({
  id: "rt-1",
  workspace_id: "ws-1",
  daemon_id: null,
  name: "Workstation",
  runtime_mode: "local",
  provider: "claude",
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
        members={members}
        currentUserId="user-1"
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />,
    );
    expect(screen.getByText(/register a runtime/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^create$/i })).toBeDisabled();
  });
});
