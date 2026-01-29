import { Agent as PiAgentCore, type AgentEvent } from "@mariozechner/pi-agent-core";
import type { AgentOptions, AgentRunResult } from "./types.js";
import { createAgentOutput } from "./output.js";
import { resolveModel, resolveTools } from "./tools.js";

export class Agent {
  private readonly agent: PiAgentCore;
  private readonly output;

  constructor(options: AgentOptions = {}) {
    const stdout = options.logger?.stdout ?? process.stdout;
    const stderr = options.logger?.stderr ?? process.stderr;
    this.output = createAgentOutput({ stdout, stderr });

    this.agent = new PiAgentCore();
    if (options.systemPrompt) this.agent.setSystemPrompt(options.systemPrompt);
    if (options.thinkingLevel) this.agent.setThinkingLevel(options.thinkingLevel);

    this.agent.setModel(resolveModel(options));
    this.agent.setTools(resolveTools(options));
    this.agent.subscribe((event: AgentEvent) => this.output.handleEvent(event));
  }

  async run(prompt: string): Promise<AgentRunResult> {
    this.output.state.lastAssistantText = "";
    await this.agent.prompt(prompt);
    return { text: this.output.state.lastAssistantText, error: this.agent.state.error };
  }
}
