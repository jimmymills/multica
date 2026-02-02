/** Hello Action - test greeting message */

export const HelloAction = "hello" as const;
export const HelloResponseAction = "hello_response" as const;

/** Hello request payload */
export interface HelloPayload {
  greeting: string;
}

/** Hello response payload */
export interface HelloResponsePayload {
  reply: string;
}
