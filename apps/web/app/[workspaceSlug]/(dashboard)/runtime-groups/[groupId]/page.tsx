"use client";

import { use } from "react";
import { RuntimeGroupDetailPage } from "@multica/views/runtime-groups/runtime-group-detail-page";

export default function Page({
  params,
}: {
  params: Promise<{ groupId: string }>;
}) {
  const { groupId } = use(params);
  return <RuntimeGroupDetailPage groupId={groupId} />;
}
