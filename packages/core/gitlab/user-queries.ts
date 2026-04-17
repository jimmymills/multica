import { useQuery, type UseQueryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { UserGitlabConnection } from "./user-types";

export const userGitlabKeys = {
  all: (wsId: string) => ["gitlab", "user", wsId] as const,
  connection: (wsId: string) => [...userGitlabKeys.all(wsId), "connection"] as const,
};

export function userGitlabConnectionOptions(wsId: string) {
  return {
    queryKey: userGitlabKeys.connection(wsId),
    queryFn: () => api.getUserGitlabConnection(wsId),
    retry: false,
  } satisfies UseQueryOptions<UserGitlabConnection>;
}

export function useUserGitlabConnection(wsId: string) {
  return useQuery(userGitlabConnectionOptions(wsId));
}
