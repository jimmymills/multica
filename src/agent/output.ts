import type { AgentEvent, AgentMessage } from "@mariozechner/pi-agent-core";

export type AgentOutputState = {
  lastAssistantText: string;
  printedLen: number;
  streaming: boolean;
};

export type AgentOutput = {
  state: AgentOutputState;
  handleEvent: (event: AgentEvent) => void;
};

function extractText(message: AgentMessage | undefined): string {
  if (!message || typeof message !== "object" || !("content" in message)) return "";
  const content = (message as { content?: Array<{ type: string; text?: string }> }).content;
  if (!Array.isArray(content)) return "";
  return content
    .filter((c) => c.type === "text")
    .map((c) => c.text ?? "")
    .join("");
}

function toolDisplayName(name: string): string {
  const map: Record<string, string> = {
    read: "ReadFile",
    write: "WriteFile",
    edit: "EditFile",
    bash: "Bash",
    grep: "Grep",
    find: "FindFiles",
    ls: "ListDir",
  };
  return map[name] || name;
}

function formatToolArgs(name: string, args: unknown): string {
  if (!args || typeof args !== "object") return "";
  const record = args as Record<string, unknown>;
  const get = (key: string) => (record[key] !== undefined ? String(record[key]) : "");
  switch (name) {
    case "read":
      return get("path") || get("file");
    case "write":
      return get("path") || get("file");
    case "edit":
      return get("path") || get("file");
    case "grep":
      return [get("pattern"), get("path") || get("directory")].filter(Boolean).join(" ");
    case "find":
      return [get("glob") || get("pattern"), get("path") || get("directory")].filter(Boolean).join(" ");
    case "ls":
      return get("path") || get("directory");
    case "bash":
      return get("command");
    default:
      return "";
  }
}

function formatToolLine(name: string, args: unknown): string {
  const title = toolDisplayName(name);
  const argText = formatToolArgs(name, args);
  return argText ? `• Used ${title} (${argText})` : `• Used ${title}`;
}

export function createAgentOutput(params: {
  stdout: NodeJS.WritableStream;
  stderr: NodeJS.WritableStream;
}): AgentOutput {
  const state: AgentOutputState = {
    lastAssistantText: "",
    printedLen: 0,
    streaming: false,
  };

  const handleEvent = (event: AgentEvent) => {
    switch (event.type) {
      case "message_start": {
        const msg = event.message;
        if (msg.role === "assistant") {
          state.streaming = true;
          state.printedLen = 0;
          const text = extractText(msg);
          if (text.length > 0) {
            params.stdout.write(text);
            state.printedLen = text.length;
          }
        }
        break;
      }
      case "message_update": {
        const msg = event.message;
        if (msg.role === "assistant") {
          const text = extractText(msg);
          if (text.length > state.printedLen) {
            params.stdout.write(text.slice(state.printedLen));
            state.printedLen = text.length;
          }
        }
        break;
      }
      case "message_end": {
        const msg = event.message;
        if (msg.role === "assistant") {
          const text = extractText(msg);
          if (text.length > state.printedLen) {
            params.stdout.write(text.slice(state.printedLen));
            state.printedLen = text.length;
          }
          if (state.streaming) params.stdout.write("\n");
          state.streaming = false;
          state.lastAssistantText = text;
        }
        break;
      }
      case "tool_execution_start":
        params.stderr.write(`${formatToolLine(event.toolName, event.args)}\n`);
        break;
      case "tool_execution_end":
        if (event.isError) {
          const errorText = extractText(event.result) || "Tool failed";
          params.stderr.write(`• Tool error (${toolDisplayName(event.toolName)}): ${errorText}\n`);
        }
        break;
      default:
        break;
    }
  };

  return { state, handleEvent };
}
