import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { RuntimeGroupDetail } from "./runtime-group-detail";
import type { RuntimeGroup, RuntimeDevice } from "@multica/core/types";

const member = {
  id: "r1",
  name: "Workstation",
  status: "online" as const,
  runtime_mode: "local" as const,
  provider: "claude",
  device_info: "",
  owner_id: null,
  last_used_at: null,
};

const runtime: RuntimeDevice = {
  id: "r1",
  workspace_id: "ws",
  daemon_id: null,
  name: "Workstation",
  runtime_mode: "local",
  provider: "claude",
  status: "online",
  device_info: "",
  metadata: {},
  owner_id: null,
  last_seen_at: null,
  created_at: "",
  updated_at: "",
};

const group = (overrides: Partial<RuntimeGroup> = {}): RuntimeGroup => ({
  id: "g1",
  workspace_id: "ws",
  name: "Team",
  description: "",
  runtimes: [member],
  active_override: null,
  member_agent_count: 0,
  created_by: null,
  created_at: "",
  updated_at: "",
  ...overrides,
});

describe("RuntimeGroupDetail", () => {
  it("shows Set override button when no active override", () => {
    render(
      <RuntimeGroupDetail
        group={group()}
        runtimes={[runtime]}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
        onSetOverride={vi.fn()}
        onClearOverride={vi.fn()}
      />,
    );
    expect(screen.getByRole("button", { name: /set override/i })).toBeInTheDocument();
  });

  it("shows active override card and Cancel button when override is set", () => {
    const g = group({
      active_override: {
        id: "o",
        group_id: "g1",
        runtime_id: "r1",
        runtime_name: "Workstation",
        starts_at: "",
        ends_at: new Date(Date.now() + 3600_000).toISOString(),
        created_by: null,
      },
    });
    render(
      <RuntimeGroupDetail
        group={g}
        runtimes={[runtime]}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
        onSetOverride={vi.fn()}
        onClearOverride={vi.fn()}
      />,
    );
    expect(screen.getByText(/Using/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /cancel override/i })).toBeInTheDocument();
  });

  it("disables Save when name cleared", () => {
    render(
      <RuntimeGroupDetail
        group={group()}
        runtimes={[runtime]}
        onUpdate={vi.fn()}
        onDelete={vi.fn()}
        onSetOverride={vi.fn()}
        onClearOverride={vi.fn()}
      />,
    );
    const nameInput = screen.getByDisplayValue("Team");
    fireEvent.change(nameInput, { target: { value: "" } });
    expect(screen.getByRole("button", { name: /save changes/i })).toBeDisabled();
  });
});
