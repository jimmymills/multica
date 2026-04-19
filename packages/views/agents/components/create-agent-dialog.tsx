"use client";

import { useState, useMemo } from "react";
import { Globe, Lock, Loader2 } from "lucide-react";
import { ProviderLogo } from "../../runtimes/components/provider-logo";
import { ActorAvatar } from "../../common/actor-avatar";
import type {
  AgentVisibility,
  RuntimeDevice,
  MemberWithUser,
  RuntimeGroup,
  CreateAgentRequest,
} from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@multica/ui/components/ui/popover";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { toast } from "sonner";

type RuntimeFilter = "mine" | "all";

export function CreateAgentDialog({
  runtimes,
  runtimesLoading,
  groups,
  members,
  currentUserId,
  onClose,
  onCreate,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  groups: RuntimeGroup[];
  members: MemberWithUser[];
  currentUserId: string | null;
  onClose: () => void;
  onCreate: (data: CreateAgentRequest) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<AgentVisibility>("private");
  const [creating, setCreating] = useState(false);
  const [addRuntimeOpen, setAddRuntimeOpen] = useState(false);
  const [addGroupOpen, setAddGroupOpen] = useState(false);
  const [selectedGroupIds, setSelectedGroupIds] = useState<string[]>([]);
  const [runtimeFilter, setRuntimeFilter] = useState<RuntimeFilter>("mine");

  const getOwnerMember = (ownerId: string | null) => {
    if (!ownerId) return null;
    return members.find((m) => m.user_id === ownerId) ?? null;
  };

  const hasOtherRuntimes = runtimes.some((r) => r.owner_id !== currentUserId);

  const filteredRuntimes = useMemo(() => {
    const filtered = runtimeFilter === "mine" && currentUserId
      ? runtimes.filter((r) => r.owner_id === currentUserId)
      : runtimes;
    return [...filtered].sort((a, b) => {
      if (a.owner_id === currentUserId && b.owner_id !== currentUserId) return -1;
      if (a.owner_id !== currentUserId && b.owner_id === currentUserId) return 1;
      return 0;
    });
  }, [runtimes, runtimeFilter, currentUserId]);

  const [selectedRuntimeIds, setSelectedRuntimeIds] = useState<string[]>(() =>
    filteredRuntimes[0] ? [filteredRuntimes[0].id] : [],
  );

  const selectedRuntimes = useMemo(
    () => selectedRuntimeIds
      .map((id) => runtimes.find((r) => r.id === id))
      .filter((r): r is RuntimeDevice => Boolean(r)),
    [selectedRuntimeIds, runtimes],
  );

  const candidateRuntimes = useMemo(
    () => filteredRuntimes.filter((r) => !selectedRuntimeIds.includes(r.id)),
    [filteredRuntimes, selectedRuntimeIds],
  );

  const selectedGroupObjects = useMemo(
    () => selectedGroupIds.map((id) => groups.find((g) => g.id === id)).filter(Boolean) as RuntimeGroup[],
    [selectedGroupIds, groups],
  );
  const candidateGroups = useMemo(
    () => groups.filter((g) => !selectedGroupIds.includes(g.id)),
    [groups, selectedGroupIds],
  );

  const canSubmit = !creating && name.trim().length > 0 && (selectedRuntimeIds.length + selectedGroupIds.length) > 0;

  const handleSubmit = async () => {
    if (!canSubmit) return;
    setCreating(true);
    try {
      await onCreate({
        name: name.trim(),
        description: description.trim(),
        runtime_ids: selectedRuntimeIds,
        group_ids: selectedGroupIds,
        visibility,
      });
      onClose();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to create agent");
      setCreating(false);
    }
  };

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Create Agent</DialogTitle>
          <DialogDescription>
            Create a new AI agent for your workspace.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 min-w-0">
          <div>
            <Label className="text-xs text-muted-foreground">Name</Label>
            <Input
              autoFocus
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Deep Research Agent"
              className="mt-1"
              onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
            />
          </div>

          <div>
            <Label className="text-xs text-muted-foreground">Description</Label>
            <Input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What does this agent do?"
              className="mt-1"
            />
          </div>

          <div>
            <Label className="text-xs text-muted-foreground">Visibility</Label>
            <div className="mt-1.5 flex gap-2">
              <button
                type="button"
                onClick={() => setVisibility("workspace")}
                className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2.5 text-sm transition-colors ${
                  visibility === "workspace"
                    ? "border-primary bg-primary/5"
                    : "border-border hover:bg-muted"
                }`}
              >
                <Globe className="h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="text-left">
                  <div className="font-medium">Workspace</div>
                  <div className="text-xs text-muted-foreground">All members can assign</div>
                </div>
              </button>
              <button
                type="button"
                onClick={() => setVisibility("private")}
                className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2.5 text-sm transition-colors ${
                  visibility === "private"
                    ? "border-primary bg-primary/5"
                    : "border-border hover:bg-muted"
                }`}
              >
                <Lock className="h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="text-left">
                  <div className="font-medium">Private</div>
                  <div className="text-xs text-muted-foreground">Only you can assign</div>
                </div>
              </button>
            </div>
          </div>

          <div>
            <Label className="text-xs text-muted-foreground">Groups</Label>
            <div className="mt-1.5 flex flex-wrap gap-2">
              {selectedGroupObjects.map((g) => (
                <div key={g.id} className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-sm">
                  <span className="truncate font-medium">{g.name}</span>
                  {g.active_override && (
                    <span
                      className="shrink-0 rounded bg-amber-500/10 px-1.5 py-0.5 text-xs font-medium text-amber-600"
                      title={`Overridden to ${g.active_override.runtime_name} until ${new Date(g.active_override.ends_at).toLocaleString()}`}
                    >
                      Override
                    </span>
                  )}
                  <button
                    type="button"
                    aria-label={`Remove ${g.name}`}
                    onClick={() => setSelectedGroupIds((ids) => ids.filter((id) => id !== g.id))}
                    className="ml-1 text-muted-foreground hover:text-foreground"
                  >×</button>
                </div>
              ))}
              <Popover open={addGroupOpen} onOpenChange={setAddGroupOpen}>
                <PopoverTrigger
                  disabled={candidateGroups.length === 0}
                  className="rounded-lg border border-dashed border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
                >+ Add group</PopoverTrigger>
                <PopoverContent align="start" className="w-72 p-1 max-h-60 overflow-y-auto">
                  {candidateGroups.map((g) => (
                    <button
                      key={g.id}
                      role="menuitem"
                      onClick={() => { setSelectedGroupIds((ids) => [...ids, g.id]); setAddGroupOpen(false); }}
                      className="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm hover:bg-accent/50"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-medium">{g.name}</div>
                        <div className="truncate text-xs text-muted-foreground">
                          {g.runtimes.length} runtime{g.runtimes.length === 1 ? "" : "s"}
                        </div>
                      </div>
                    </button>
                  ))}
                </PopoverContent>
              </Popover>
            </div>
          </div>

          <div className="min-w-0">
            <div className="flex items-center justify-between">
              <Label className="text-xs text-muted-foreground">Runtimes</Label>
              {hasOtherRuntimes && (
                <div className="flex items-center gap-0.5 rounded-md bg-muted p-0.5">
                  <button
                    type="button"
                    onClick={() => setRuntimeFilter("mine")}
                    className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                      runtimeFilter === "mine"
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    Mine
                  </button>
                  <button
                    type="button"
                    onClick={() => setRuntimeFilter("all")}
                    className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
                      runtimeFilter === "all"
                        ? "bg-background text-foreground shadow-sm"
                        : "text-muted-foreground hover:text-foreground"
                    }`}
                  >
                    All
                  </button>
                </div>
              )}
            </div>

            {runtimesLoading ? (
              <div className="mt-1.5 flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2.5 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 shrink-0 animate-spin" />
                Loading runtimes…
              </div>
            ) : (
              <div className="mt-1.5 flex flex-wrap gap-2">
                {selectedRuntimes.map((device) => (
                  <div
                    key={device.id}
                    className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-sm"
                  >
                    <ProviderLogo provider={device.provider} className="h-4 w-4 shrink-0" />
                    <span className="truncate font-medium">{device.name}</span>
                    {device.runtime_mode === "cloud" && (
                      <span className="shrink-0 rounded bg-info/10 px-1.5 py-0.5 text-xs font-medium text-info">
                        Cloud
                      </span>
                    )}
                    <span
                      className={`h-2 w-2 shrink-0 rounded-full ${
                        device.status === "online" ? "bg-success" : "bg-muted-foreground/40"
                      }`}
                    />
                    <button
                      type="button"
                      aria-label={`Remove ${device.name}`}
                      onClick={() =>
                        setSelectedRuntimeIds((ids) => ids.filter((id) => id !== device.id))
                      }
                      className="ml-1 text-muted-foreground hover:text-foreground"
                    >
                      ×
                    </button>
                  </div>
                ))}

                <Popover open={addRuntimeOpen} onOpenChange={setAddRuntimeOpen}>
                  <PopoverTrigger
                    disabled={candidateRuntimes.length === 0}
                    className="rounded-lg border border-dashed border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
                  >
                    + Add runtime
                  </PopoverTrigger>
                  <PopoverContent align="start" className="w-72 p-1 max-h-60 overflow-y-auto">
                    {candidateRuntimes.map((device) => {
                      const ownerMember = getOwnerMember(device.owner_id);
                      return (
                        <button
                          key={device.id}
                          role="menuitem"
                          onClick={() => {
                            setSelectedRuntimeIds((ids) => [...ids, device.id]);
                            setAddRuntimeOpen(false);
                          }}
                          className="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm hover:bg-accent/50"
                        >
                          <ProviderLogo provider={device.provider} className="h-4 w-4 shrink-0" />
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                              <span className="truncate font-medium">{device.name}</span>
                              {device.runtime_mode === "cloud" && (
                                <span className="shrink-0 rounded bg-info/10 px-1.5 py-0.5 text-xs font-medium text-info">
                                  Cloud
                                </span>
                              )}
                            </div>
                            <div className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
                              {ownerMember ? (
                                <>
                                  <ActorAvatar actorType="member" actorId={ownerMember.user_id} size={14} />
                                  <span className="truncate">{ownerMember.name}</span>
                                </>
                              ) : (
                                <span className="truncate">{device.device_info}</span>
                              )}
                            </div>
                          </div>
                          <span
                            className={`h-2 w-2 shrink-0 rounded-full ${
                              device.status === "online" ? "bg-success" : "bg-muted-foreground/40"
                            }`}
                          />
                        </button>
                      );
                    })}
                  </PopoverContent>
                </Popover>
              </div>
            )}

            {!runtimesLoading && selectedRuntimeIds.length === 0 && selectedGroupIds.length === 0 && (
              <p className="mt-2 text-xs text-destructive">
                {runtimes.length === 0
                  ? "Register a runtime before creating an agent."
                  : "At least one runtime is required."}
              </p>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit}>
            {creating ? "Creating..." : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
