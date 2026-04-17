"use client";

import { GitBranch } from "lucide-react";
import { SettingsPage, GitlabTab, type ExtraSettingsTab } from "@multica/views/settings";

const gitlabEnabled = process.env.NEXT_PUBLIC_GITLAB_ENABLED === "true";

const extraWorkspaceTabs: ExtraSettingsTab[] = gitlabEnabled
  ? [
      {
        value: "gitlab",
        label: "GitLab",
        icon: GitBranch,
        content: <GitlabTab />,
      },
    ]
  : [];

export default function SettingsRoute() {
  return <SettingsPage extraWorkspaceTabs={extraWorkspaceTabs} />;
}
