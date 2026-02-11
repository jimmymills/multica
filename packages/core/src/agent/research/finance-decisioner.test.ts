import { describe, expect, it } from "vitest";
import { buildInternalFinanceGuidance, decideFinanceEvidencePlan } from "./finance-decisioner.js";

describe("decideFinanceEvidencePlan", () => {
  it("returns undefined for non-finance prompts", () => {
    const result = decideFinanceEvidencePlan({
      prompt: "Write a TypeScript utility to parse CSV files.",
      tools: ["read", "write", "exec"],
    });
    expect(result).toBeUndefined();
  });

  it("prefers data_only for secondary market non-event tasks", () => {
    const result = decideFinanceEvidencePlan({
      prompt: "Analyze AAPL valuation based on 5-year financial statements.",
      tools: ["data", "web_search", "web_fetch"],
    });
    expect(result).toBeDefined();
    expect(result?.plan).toBe("data_only");
    expect(result?.marketRoute).toBe("secondary");
    expect(result?.reasons).toContain("secondary_market_task");
  });

  it("prefers hybrid for event-driven secondary tasks", () => {
    const result = decideFinanceEvidencePlan({
      prompt: "Why did AAPL drop after latest earnings and guidance update?",
      tools: ["data", "web_search", "web_fetch"],
    });
    expect(result).toBeDefined();
    expect(result?.plan).toBe("hybrid");
    expect(result?.reasons).toContain("event_driven");
    expect(result?.reasons).toContain("causal_explanation_needed");
  });

  it("prefers web_first for primary market tasks without ticker", () => {
    const result = decideFinanceEvidencePlan({
      prompt: "Review this pre-IPO issuance structure and lock-up risks.",
      tools: ["data", "web_search", "web_fetch"],
    });
    expect(result).toBeDefined();
    expect(result?.marketRoute).toBe("primary_no_ticker");
    expect(result?.plan).toBe("web_first");
  });

  it("degrades when web tools are unavailable for event-driven tasks", () => {
    const result = decideFinanceEvidencePlan({
      prompt: "Analyze latest earnings surprise drivers for TSLA stock.",
      tools: ["data"],
    });
    expect(result).toBeDefined();
    expect(result?.reasons).toContain("web_tools_unavailable");
    expect(result?.confidencePenalty).toBe("high");
  });
});

describe("buildInternalFinanceGuidance", () => {
  it("formats internal guidance text", () => {
    const decision = decideFinanceEvidencePlan({
      prompt: "Analyze latest AAPL earnings impact on valuation.",
      tools: ["data", "web_search"],
    });
    expect(decision).toBeDefined();
    const guidance = buildInternalFinanceGuidance(decision!);
    expect(guidance).toContain("Internal Finance Research Guidance");
    expect(guidance).toContain("Preferred evidence plan:");
    expect(guidance).toContain("Do not expose technical labels");
  });
});
