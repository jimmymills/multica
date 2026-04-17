import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { userGitlabKeys } from "./user-queries";
import type { ConnectUserGitlabInput } from "./user-types";

export function useConnectUserGitlabMutation(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ConnectUserGitlabInput) => api.connectUserGitlab(wsId, input),
    onSuccess: (data) => {
      qc.setQueryData(userGitlabKeys.connection(wsId), data);
    },
  });
}

export function useDisconnectUserGitlabMutation(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.disconnectUserGitlab(wsId),
    onSuccess: () => {
      qc.setQueryData(userGitlabKeys.connection(wsId), { connected: false });
    },
  });
}
