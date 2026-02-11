import type { AgentMessage } from "@mariozechner/pi-agent-core";
import type { UserMessage } from "@mariozechner/pi-ai";

export type SessionMeta = {
  provider?: string;
  model?: string;
  thinkingLevel?: string;
  /** Reasoning mode: off, on, stream */
  reasoningMode?: string;
  /** Context window token 数 */
  contextWindowTokens?: number;
  /** Internal finance evidence decision from the latest run */
  researchDecision?: {
    domain: "finance";
    plan: "data_only" | "hybrid" | "web_first";
    marketRoute: "secondary" | "primary_with_ticker" | "primary_no_ticker";
    confidencePenalty: "low" | "medium" | "high";
    reasons: string[];
    timestamp: number;
  } | undefined;
};

export type SessionEntry =
  | {
      type: "message";
      message: AgentMessage;
      timestamp: number;
      internal?: boolean;
      /**
       * User-visible content preserved for UI/history rendering.
       * When omitted, consumers should fall back to message.content.
       */
      displayContent?: UserMessage["content"];
    }
  | { type: "meta"; meta: SessionMeta; timestamp: number }
  | {
      type: "compaction";
      removed: number;
      kept: number;
      timestamp: number;
      /** Token 感知 compaction 信息（可选，向后兼容） */
      tokensRemoved?: number | undefined;
      tokensKept?: number | undefined;
      /** 摘要模式生成的摘要 */
      summary?: string | undefined;
      reason?: "count" | "tokens" | "summary" | "pruning" | undefined;
    };
