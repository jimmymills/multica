// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, act, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import {
  useConnectUserGitlabMutation,
  useDisconnectUserGitlabMutation,
} from "./user-mutations";

vi.mock("../api", () => ({
  api: {
    getUserGitlabConnection: vi.fn(),
    connectUserGitlab: vi.fn(),
    disconnectUserGitlab: vi.fn(),
  },
}));

import { api } from "../api";

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe("user gitlab mutations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("connectUserGitlab calls api and caches the response", async () => {
    const conn = { connected: true, gitlab_user_id: 555, gitlab_username: "alice" };
    (api.connectUserGitlab as ReturnType<typeof vi.fn>).mockResolvedValue(conn);

    const { result } = renderHook(() => useConnectUserGitlabMutation("ws-1"), { wrapper: wrapper() });
    await act(async () => {
      await result.current.mutateAsync({ token: "glpat-x" });
    });
    expect(api.connectUserGitlab).toHaveBeenCalledWith("ws-1", { token: "glpat-x" });
    await waitFor(() => expect(result.current.data).toEqual(conn));
  });

  it("disconnectUserGitlab calls api and clears cache to {connected:false}", async () => {
    (api.disconnectUserGitlab as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    const { result } = renderHook(() => useDisconnectUserGitlabMutation("ws-1"), { wrapper: wrapper() });
    await act(async () => {
      await result.current.mutateAsync();
    });
    expect(api.disconnectUserGitlab).toHaveBeenCalledWith("ws-1");
  });
});
