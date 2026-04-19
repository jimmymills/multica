"use client";

import { useState } from "react";
import type { AgentRuntimeRef } from "@multica/core/types";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Label } from "@multica/ui/components/ui/label";
import { Input } from "@multica/ui/components/ui/input";

const PRESETS: { label: string; hours: number }[] = [
  { label: "1 day", hours: 24 },
  { label: "2 days", hours: 48 },
  { label: "1 week", hours: 24 * 7 },
  { label: "2 weeks", hours: 24 * 14 },
];

export function SetOverrideDialog({
  members,
  currentRuntimeId,
  onClose,
  onSubmit,
}: {
  members: AgentRuntimeRef[];
  currentRuntimeId: string | null;
  onClose: () => void;
  onSubmit: (req: { runtime_id: string; ends_at: string }) => Promise<void>;
}) {
  const [runtimeId, setRuntimeId] = useState(currentRuntimeId ?? members[0]?.id ?? "");
  const [hours, setHours] = useState<number>(24);
  const [customISO, setCustomISO] = useState("");
  const [useCustom, setUseCustom] = useState(false);
  const [saving, setSaving] = useState(false);

  const handleSubmit = async () => {
    if (!runtimeId) return;
    const endsAt = useCustom
      ? new Date(customISO).toISOString()
      : new Date(Date.now() + hours * 60 * 60 * 1000).toISOString();
    setSaving(true);
    try {
      await onSubmit({ runtime_id: runtimeId, ends_at: endsAt });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Set priority override</DialogTitle>
          <DialogDescription>
            Agents using this group will prefer the chosen runtime while it&apos;s online.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <Label className="text-xs text-muted-foreground">Runtime</Label>
            <select
              value={runtimeId}
              onChange={(e) => setRuntimeId(e.target.value)}
              className="mt-1 w-full rounded-md border border-border bg-background px-3 py-2 text-sm"
            >
              {members.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <Label className="text-xs text-muted-foreground">Duration</Label>
            <div className="mt-1.5 flex flex-wrap gap-2">
              {PRESETS.map((p) => (
                <button
                  key={p.hours}
                  type="button"
                  onClick={() => {
                    setUseCustom(false);
                    setHours(p.hours);
                  }}
                  className={`rounded-lg border px-3 py-1.5 text-sm ${
                    !useCustom && hours === p.hours
                      ? "border-primary bg-primary/5"
                      : "border-border hover:bg-muted"
                  }`}
                >
                  {p.label}
                </button>
              ))}
              <button
                type="button"
                onClick={() => setUseCustom(true)}
                className={`rounded-lg border px-3 py-1.5 text-sm ${
                  useCustom ? "border-primary bg-primary/5" : "border-border hover:bg-muted"
                }`}
              >
                Custom…
              </button>
            </div>
            {useCustom && (
              <Input
                type="datetime-local"
                value={customISO}
                onChange={(e) => setCustomISO(e.target.value)}
                className="mt-2"
              />
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={saving || !runtimeId}>
            {saving ? "Saving..." : "Set override"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
