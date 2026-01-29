import type { AgentOptions } from "./types.js";
import { getModel } from "@mariozechner/pi-ai";
import { createCodingTools } from "@mariozechner/pi-coding-agent";

export function resolveModel(options: AgentOptions) {
  if (options.provider && options.model) {
    return getModel(options.provider, options.model);
  }
  return getModel("kimi-coding", "kimi-k2-thinking");
}

export function resolveTools(options: AgentOptions) {
  const cwd = options.cwd ?? process.cwd();
  return createCodingTools(cwd);
}
