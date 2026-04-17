export interface UserGitlabConnection {
  connected: boolean;
  gitlab_user_id?: number;
  gitlab_username?: string;
}

export interface ConnectUserGitlabInput {
  token: string;
}
