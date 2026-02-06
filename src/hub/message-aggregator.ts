/**
 * Message aggregator for buffering streaming agent events
 * and emitting complete "block replies" at natural text boundaries.
 *
 * Used for third-party messaging integrations (Discord, Telegram)
 * that cannot consume raw streaming deltas.
 */

import type { AgentEvent } from "@mariozechner/pi-agent-core";
import type { MulticaEvent } from "../agent/events.js";
import { extractText } from "../agent/extract-text.js";
import { BlockChunker, DEFAULT_CHUNKER_CONFIG, type BlockChunkerConfig } from "./block-chunker.js";

/** A completed text block emitted by the aggregator */
export interface BlockReply {
  /** 0-based sequence number within the current message */
  index: number;
  /** The text content of this block */
  text: string;
  /** Whether this is the final block in the current message */
  isFinal: boolean;
}

/** Callback for receiving aggregated block replies */
export type BlockReplyCallback = (block: BlockReply) => void;

/** Callback for pass-through events (tool, compaction, lifecycle) */
export type PassthroughCallback = (event: AgentEvent | MulticaEvent) => void;

export { type BlockChunkerConfig, DEFAULT_CHUNKER_CONFIG };

export class MessageAggregator {
  private buffer = "";
  private previousText = "";
  private blockIndex = 0;
  private readonly chunker: BlockChunker;

  constructor(
    config: BlockChunkerConfig,
    private readonly onBlock: BlockReplyCallback,
    private readonly onPassthrough: PassthroughCallback,
  ) {
    this.chunker = new BlockChunker(config);
  }

  /**
   * Feed an agent event into the aggregator.
   * Text content from message_update events is buffered and emitted as block replies.
   * All other events are passed through immediately.
   */
  handleEvent(event: AgentEvent | MulticaEvent): void {
    switch (event.type) {
      case "compaction_start":
      case "compaction_end":
      case "tool_execution_start":
      case "tool_execution_end":
      case "tool_execution_update":
        this.onPassthrough(event);
        return;

      case "message_start":
        this.resetState();
        this.onPassthrough(event);
        return;

      case "message_update":
        this.handleMessageUpdate(event as AgentEvent & { type: "message_update" });
        return;

      case "message_end":
        this.handleMessageEnd(event as AgentEvent & { type: "message_end" });
        this.onPassthrough(event);
        return;

      default:
        // agent_start, agent_end, turn_start, turn_end, etc.
        this.onPassthrough(event);
        return;
    }
  }

  /** Reset all buffering state (e.g. between messages or for external use) */
  reset(): void {
    this.resetState();
  }

  private handleMessageUpdate(event: AgentEvent): void {
    const message = (event as { message?: unknown }).message;
    const currentText = extractText(message as Parameters<typeof extractText>[0]);
    this.appendDelta(currentText);

    // Try to emit chunks from buffer
    let result = this.chunker.tryChunk(this.buffer);
    while (result !== null) {
      this.emitBlock(result.chunk, false);
      this.buffer = result.remainder;
      result = this.chunker.tryChunk(this.buffer);
    }
  }

  private handleMessageEnd(event: AgentEvent): void {
    const message = (event as { message?: unknown }).message;
    const currentText = extractText(message as Parameters<typeof extractText>[0]);
    this.appendDelta(currentText);
    this.flushBuffer();
  }

  private appendDelta(currentText: string): void {
    // Compute incremental delta (monotonic accumulation)
    if (currentText.length <= this.previousText.length) return;
    const delta = currentText.slice(this.previousText.length);

    this.previousText = currentText;
    this.buffer += delta;
  }

  private flushBuffer(): void {
    const result = this.chunker.flush(this.buffer);
    if (result) {
      this.emitBlock(result.chunk, true);
      this.buffer = result.remainder;
    }
  }

  private emitBlock(text: string, isFinal: boolean): void {
    this.onBlock({
      index: this.blockIndex++,
      text,
      isFinal,
    });
  }

  private resetState(): void {
    this.buffer = "";
    this.previousText = "";
    this.blockIndex = 0;
  }
}
