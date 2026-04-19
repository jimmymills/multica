"use client";

import { useEffect, useState, useMemo } from "react";
import { Trash2 } from "lucide-react";
import type { RuntimeGroup, RuntimeDevice } from "@multica/core/types";
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
import { SetOverrideDialog } from "./set-override-dialog";
import { ProviderLogo } from "../runtimes/components/provider-logo";

export function RuntimeGroupDetail({
  group,
  runtimes,
  onUpdate,
  onDelete,
  onSetOverride,
  onClearOverride,
}: {
  group: RuntimeGroup;
  runtimes: RuntimeDevice[];
  onUpdate: (updates: { name?: string; description?: string; runtime_ids?: string[] }) => Promise<void>;
  onDelete: () => Promise<void>;
  onSetOverride: (req: { runtime_id: string; ends_at: string }) => Promise<void>;
  onClearOverride: () => Promise<void>;
}) {
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description);
  const [selectedRuntimeIds, setSelectedRuntimeIds] = useState<string[]>(
    group.runtimes.map((r) => r.id),
  );
  const [addOpen, setAddOpen] = useState(false);
  const [overrideOpen, setOverrideOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  // Keep local state in sync if the server pushes updates.
  useEffect(() => {
    setName(group.name);
    setDescription(group.description);
    setSelectedRuntimeIds(group.runtimes.map((r) => r.id));
  }, [group.id, group.name, group.description, group.runtimes]);

  const selected = useMemo(
    () =>
      selectedRuntimeIds
        .map((id) => runtimes.find((r) => r.id === id) ?? group.runtimes.find((r) => r.id === id))
        .filter(Boolean) as (RuntimeDevice | RuntimeGroup["runtimes"][number])[],
    [selectedRuntimeIds, runtimes, group.runtimes],
  );

  const candidates = useMemo(
    () => runtimes.filter((r) => !selectedRuntimeIds.includes(r.id)),
    [runtimes, selectedRuntimeIds],
  );

  const originalRuntimeIds = useMemo(
    () => new Set(group.runtimes.map((r) => r.id)),
    [group.runtimes],
  );
  const runtimesDirty =
    selectedRuntimeIds.length !== originalRuntimeIds.size ||
    selectedRuntimeIds.some((id) => !originalRuntimeIds.has(id));

  const dirty =
    name !== group.name ||
    description !== group.description ||
    runtimesDirty;

  const canSave = dirty && !saving && name.trim().length > 0 && selectedRuntimeIds.length > 0;

  const handleSave = async () => {
    if (!canSave) return;
    setSaving(true);
    try {
      await onUpdate({
        name: name.trim() !== group.name ? name.trim() : undefined,
        description: description !== group.description ? description : undefined,
        runtime_ids: selectedRuntimeIds,
      });
      toast.success("Group saved");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to save group");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="p-6 max-w-2xl space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">{group.name}</h1>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => setConfirmDelete(true)}
          aria-label="Delete group"
        >
          <Trash2 className="h-4 w-4 text-destructive" />
        </Button>
      </div>

      <div>
        <Label className="text-xs text-muted-foreground">Name</Label>
        <Input value={name} onChange={(e) => setName(e.target.value)} className="mt-1" />
      </div>

      <div>
        <Label className="text-xs text-muted-foreground">Description</Label>
        <Input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          className="mt-1"
        />
      </div>

      <div>
        <Label className="text-xs text-muted-foreground">Runtimes</Label>
        <div className="mt-1.5 flex flex-wrap gap-2">
          {selected.map((d) => (
            <div
              key={d.id}
              className="flex items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-sm"
            >
              <ProviderLogo provider={d.provider} className="h-4 w-4 shrink-0" />
              <span className="truncate font-medium">{d.name}</span>
              <button
                type="button"
                aria-label={`Remove ${d.name}`}
                onClick={() =>
                  setSelectedRuntimeIds((ids) => ids.filter((id) => id !== d.id))
                }
                className="ml-1 text-muted-foreground hover:text-foreground"
              >
                ×
              </button>
            </div>
          ))}
          <Popover open={addOpen} onOpenChange={setAddOpen}>
            <PopoverTrigger
              disabled={candidates.length === 0}
              className="rounded-lg border border-dashed border-border bg-background px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted disabled:pointer-events-none disabled:opacity-50"
            >
              + Add runtime
            </PopoverTrigger>
            <PopoverContent align="start" className="w-72 p-1 max-h-60 overflow-y-auto">
              {candidates.map((d) => (
                <button
                  key={d.id}
                  role="menuitem"
                  onClick={() => {
                    setSelectedRuntimeIds((ids) => [...ids, d.id]);
                    setAddOpen(false);
                  }}
                  className="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm hover:bg-accent/50"
                >
                  <ProviderLogo provider={d.provider} className="h-4 w-4 shrink-0" />
                  <span className="truncate font-medium">{d.name}</span>
                </button>
              ))}
            </PopoverContent>
          </Popover>
        </div>
      </div>

      <div className="rounded-lg border border-border p-4">
        <Label className="text-sm font-medium">Priority override</Label>
        {group.active_override ? (
          <div className="mt-2">
            <div className="text-sm">
              Using <span className="font-medium">{group.active_override.runtime_name}</span> as
              primary until{" "}
              <span className="font-medium">
                {new Date(group.active_override.ends_at).toLocaleString()}
              </span>
            </div>
            <div className="mt-3 flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setOverrideOpen(true)}>
                Change
              </Button>
              <Button size="sm" variant="outline" onClick={() => onClearOverride()}>
                Cancel override
              </Button>
            </div>
          </div>
        ) : (
          <div className="mt-2">
            <div className="text-sm text-muted-foreground">No active override.</div>
            <div className="mt-3">
              <Button
                size="sm"
                onClick={() => setOverrideOpen(true)}
                disabled={group.runtimes.length === 0}
              >
                Set override
              </Button>
            </div>
          </div>
        )}
      </div>

      <Button onClick={handleSave} disabled={!canSave} size="sm">
        {saving ? "Saving..." : "Save changes"}
      </Button>

      {overrideOpen && (
        <SetOverrideDialog
          members={group.runtimes}
          currentRuntimeId={group.active_override?.runtime_id ?? null}
          onClose={() => setOverrideOpen(false)}
          onSubmit={async (req) => {
            await onSetOverride(req);
            setOverrideOpen(false);
          }}
        />
      )}

      {confirmDelete && (
        <ConfirmDialog
          title={`Delete ${group.name}?`}
          description={
            group.member_agent_count === 0
              ? "This permanently deletes the group."
              : `This removes the group from ${group.member_agent_count} agent${group.member_agent_count === 1 ? "" : "s"}. If any of them rely solely on this group, new tasks will fail to enqueue until you reassign runtimes.`
          }
          onCancel={() => setConfirmDelete(false)}
          onConfirm={async () => {
            await onDelete();
            setConfirmDelete(false);
          }}
        />
      )}
    </div>
  );
}

function ConfirmDialog({
  title,
  description,
  onCancel,
  onConfirm,
}: {
  title: string;
  description: string;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <Dialog open onOpenChange={(v) => { if (!v) onCancel(); }}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
