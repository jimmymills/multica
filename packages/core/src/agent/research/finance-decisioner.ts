/**
 * Finance evidence decisioner.
 *
 * Produces an internal research plan for finance tasks:
 * - data_only
 * - hybrid (data + web validation)
 * - web_first
 *
 * The output is intended for internal orchestration only.
 */

export type FinanceEvidencePlan = "data_only" | "hybrid" | "web_first";

export type FinanceMarketRoute = "secondary" | "primary_with_ticker" | "primary_no_ticker";

export type FinanceConfidencePenalty = "low" | "medium" | "high";

export interface FinanceDecisionInput {
  prompt: string;
  tools: string[];
}

export interface FinanceDecision {
  plan: FinanceEvidencePlan;
  marketRoute: FinanceMarketRoute;
  confidencePenalty: FinanceConfidencePenalty;
  reasons: string[];
  score: Record<FinanceEvidencePlan, number>;
}

const FINANCE_KEYWORDS = [
  "stock",
  "stocks",
  "equity",
  "equities",
  "valuation",
  "financial",
  "finance",
  "earnings",
  "revenue",
  "eps",
  "cash flow",
  "balance sheet",
  "income statement",
  "pe ratio",
  "market cap",
  "ipo",
  "pre-ipo",
  "listing",
  "ticker",
  "一级市场",
  "二级市场",
  "财报",
  "股票",
  "估值",
  "市值",
  "募资",
  "锁定期",
  "稀释",
];

const PRIMARY_MARKET_KEYWORDS = [
  "ipo",
  "pre-ipo",
  "prospectus",
  "s-1",
  "f-1",
  "roadshow",
  "listing",
  "follow-on",
  "new issuance",
  "lock-up",
  "dilution",
  "一级市场",
  "募资",
  "锁定期",
  "稀释",
];

const EVENT_DRIVEN_KEYWORDS = [
  "latest",
  "recent",
  "today",
  "yesterday",
  "breaking",
  "earnings call",
  "guidance",
  "surprise",
  "selloff",
  "policy",
  "fed",
  "fomc",
  "news",
  "headline",
  "突发",
  "最新",
  "消息",
  "政策",
  "财报后",
];

const CAUSAL_KEYWORDS = [
  "why",
  "reason",
  "driver",
  "impact",
  "because",
  "attribution",
  "explain",
  "原因",
  "驱动",
  "影响",
  "为什么",
];

const TIME_SENSITIVE_KEYWORDS = [
  "latest",
  "today",
  "this week",
  "this month",
  "current",
  "now",
  "最新",
  "当前",
  "近期",
];

const COMMON_UPPERCASE_NON_TICKERS = new Set([
  "IPO",
  "SEC",
  "USD",
  "CNY",
  "HKD",
  "GDP",
  "CPI",
  "PPI",
  "FED",
  "FOMC",
  "EPS",
  "FCF",
  "PE",
  "TTM",
  "DCF",
]);

function includesAny(text: string, keywords: string[]): boolean {
  return keywords.some((keyword) => text.includes(keyword));
}

function normalizeTools(tools: string[]): Set<string> {
  return new Set(tools.map((tool) => tool.toLowerCase()));
}

function hasTickerSignal(prompt: string): boolean {
  const explicit = /(?:\$|ticker\s*[:=]\s*)([A-Za-z]{1,6})/g;
  if (explicit.test(prompt)) return true;

  const upperWords = prompt.match(/\b[A-Z]{1,6}\b/g) ?? [];
  const candidates = upperWords.filter((word) => !COMMON_UPPERCASE_NON_TICKERS.has(word));
  return candidates.length > 0;
}

function isFinanceTask(prompt: string): boolean {
  const normalized = prompt.toLowerCase();
  return includesAny(normalized, FINANCE_KEYWORDS);
}

function resolveMarketRoute(prompt: string): FinanceMarketRoute {
  const normalized = prompt.toLowerCase();
  const primary = includesAny(normalized, PRIMARY_MARKET_KEYWORDS);
  if (!primary) return "secondary";
  return hasTickerSignal(prompt) ? "primary_with_ticker" : "primary_no_ticker";
}

function choosePlan(score: Record<FinanceEvidencePlan, number>): FinanceEvidencePlan {
  const order: FinanceEvidencePlan[] = ["data_only", "hybrid", "web_first"];
  let best: FinanceEvidencePlan = order[0];
  for (const plan of order) {
    if (score[plan] > score[best]) best = plan;
  }
  return best;
}

function resolveConfidencePenalty(params: {
  plan: FinanceEvidencePlan;
  hasData: boolean;
  hasWeb: boolean;
  route: FinanceMarketRoute;
  eventDriven: boolean;
  timeSensitive: boolean;
}): FinanceConfidencePenalty {
  const { plan, hasData, hasWeb, route, eventDriven, timeSensitive } = params;

  if (!hasData && !hasWeb) return "high";
  if ((plan === "hybrid" || plan === "web_first") && !hasWeb) return "high";
  if (plan === "data_only" && (eventDriven || timeSensitive) && !hasWeb) return "high";
  if (route === "primary_no_ticker") return "medium";
  if (plan === "data_only" && (eventDriven || timeSensitive)) return "medium";
  return "low";
}

export function decideFinanceEvidencePlan(input: FinanceDecisionInput): FinanceDecision | undefined {
  const { prompt } = input;
  if (!isFinanceTask(prompt)) return undefined;

  const normalized = prompt.toLowerCase();
  const toolSet = normalizeTools(input.tools);
  const hasData = toolSet.has("data");
  const hasWebSearch = toolSet.has("web_search");
  const hasWebFetch = toolSet.has("web_fetch");
  const hasWeb = hasWebSearch || hasWebFetch;

  const route = resolveMarketRoute(prompt);
  const eventDriven = includesAny(normalized, EVENT_DRIVEN_KEYWORDS);
  const causal = includesAny(normalized, CAUSAL_KEYWORDS);
  const timeSensitive = includesAny(normalized, TIME_SENSITIVE_KEYWORDS);

  const score: Record<FinanceEvidencePlan, number> = {
    data_only: hasData ? 1.0 : -3.0,
    hybrid: hasData && hasWeb ? 1.0 : -2.0,
    web_first: hasWeb ? 0.6 : -3.0,
  };

  const reasons: string[] = [];

  if (route === "secondary") {
    score.data_only += 0.7;
    score.hybrid += 0.4;
    reasons.push("secondary_market_task");
  } else if (route === "primary_with_ticker") {
    score.hybrid += 0.9;
    score.web_first += 0.3;
    score.data_only -= 0.2;
    reasons.push("primary_market_with_ticker");
  } else {
    score.web_first += 1.3;
    score.hybrid += 0.7;
    score.data_only -= 1.0;
    reasons.push("primary_market_without_ticker");
  }

  if (eventDriven) {
    score.hybrid += 0.9;
    score.web_first += 0.4;
    score.data_only -= 0.5;
    reasons.push("event_driven");
  }

  if (timeSensitive) {
    score.hybrid += 0.6;
    score.web_first += 0.3;
    score.data_only -= 0.4;
    reasons.push("time_sensitive");
  }

  if (causal) {
    score.hybrid += 0.4;
    score.web_first += 0.2;
    score.data_only -= 0.2;
    reasons.push("causal_explanation_needed");
  }

  if (!hasWeb) {
    score.hybrid -= 2.0;
    score.web_first -= 3.0;
    reasons.push("web_tools_unavailable");
  }
  if (!hasData) {
    score.data_only -= 2.5;
    score.hybrid -= 1.5;
    score.web_first += 0.5;
    reasons.push("data_tool_unavailable");
  }

  const plan = choosePlan(score);
  const confidencePenalty = resolveConfidencePenalty({
    plan,
    hasData,
    hasWeb,
    route,
    eventDriven,
    timeSensitive,
  });

  return {
    plan,
    marketRoute: route,
    confidencePenalty,
    reasons,
    score,
  };
}

export function buildInternalFinanceGuidance(decision: FinanceDecision): string {
  return [
    "## Internal Finance Research Guidance",
    "This section is internal orchestration guidance. Do not expose technical labels directly to the user unless they explicitly request methodology details.",
    `Preferred evidence plan: ${decision.plan}`,
    `Market route: ${decision.marketRoute}`,
    `Confidence penalty if evidence gaps remain: ${decision.confidencePenalty}`,
    `Decision factors: ${decision.reasons.join(", ") || "none"}`,
    "Execution policy: start with the preferred plan, then escalate evidence collection if signals conflict or causality remains unresolved.",
  ].join("\n");
}
