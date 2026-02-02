import {
  GatewayClient,
  HelloAction,
  HelloResponseAction,
  type HelloPayload,
  type HelloResponsePayload,
} from "@multica/sdk";

// 模拟一个 Client
const client = new GatewayClient({
  url: "http://localhost:3000",
  deviceId: "client-001",
  deviceType: "client",
});

// 模拟一个 Agent
const agent = new GatewayClient({
  url: "http://localhost:3000",
  deviceId: "agent-001",
  deviceType: "agent",
});

// Agent 监听消息
agent
  .onStateChange((state) => console.log("[Agent] State:", state))
  .onRegistered((deviceId) => {
    console.log("[Agent] Registered as:", deviceId);
  })
  .onMessage((message) => {
    console.log("[Agent] Received message:", message);

    // 回复消息
    if (message.action === HelloAction) {
      const payload = message.payload as HelloPayload;
      console.log("[Agent] Replying to client...");
      agent.send<HelloResponsePayload>(message.from, HelloResponseAction, {
        reply: `Hello ${message.from}! I received: "${payload.greeting}"`,
      });
    }
  })
  .onSendError((error) => console.error("[Agent] Send error:", error))
  .connect();

// Client 监听消息
client
  .onStateChange((state) => console.log("[Client] State:", state))
  .onRegistered((deviceId) => {
    console.log("[Client] Registered as:", deviceId);

    // 注册后发送消息给 Agent
    setTimeout(() => {
      console.log("[Client] Sending message to agent-001...");
      client.send<HelloPayload>("agent-001", HelloAction, {
        greeting: "Hello Agent!",
      });
    }, 500);
  })
  .onMessage((message) => {
    console.log("[Client] Received message:", message);
  })
  .onSendError((error) => console.error("[Client] Send error:", error))
  .connect();

// 5秒后断开
setTimeout(() => {
  console.log("\nClosing connections...");
  client.disconnect();
  agent.disconnect();
  process.exit(0);
}, 5000);
