const readline = require("readline");
const rl = readline.createInterface({ input: process.stdin });
const NL = String.fromCharCode(10);
const send = (o) => process.stdout.write(JSON.stringify(o) + NL);
const chunk = (text) =>
  send({ jsonrpc: "2.0", method: "session/update", params: { sessionId: "sess-1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text } } } });

rl.on("line", (line) => {
  let msg;
  try { msg = JSON.parse(line); } catch { return; }
  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { protocolVersion: 1, agentCapabilities: {} } });
  } else if (msg.method === "session/new") {
    send({ jsonrpc: "2.0", id: msg.id, result: { sessionId: "sess-1" } });
  } else if (msg.method === "session/prompt") {
    const text = msg.params.prompt.map((p) => p.text).join("");
    send({ jsonrpc: "2.0", method: "session/update", params: { sessionId: "sess-1", update: { sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "内部思考" } } } });
    send({ jsonrpc: "2.0", method: "session/update", params: { sessionId: "sess-1", update: { sessionUpdate: "tool_call", title: "Read(a.ts)" } } });
    chunk("回声：");
    chunk(text.slice(-4));
    send({ jsonrpc: "2.0", id: msg.id, result: { stopReason: "end_turn" } });
  }
});
