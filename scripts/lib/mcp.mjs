export function createMCPToolCaller({ baseUrl, token, timeoutMs = 30000 }) {
  const endpoint = `${baseUrl.replace(/\/$/, "")}/mcp`;
  let nextID = 1;
  return async function mcpTool(name, args = {}) {
    const response = await fetch(endpoint, {
      method: "POST",
      signal: AbortSignal.timeout(timeoutMs),
      headers: {
        authorization: `Bearer ${token}`,
        "content-type": "application/json"
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: nextID++,
        method: "tools/call",
        params: { name, arguments: args }
      })
    });
    const raw = await response.text();
    if (response.status !== 200) {
      throw new Error(`MCP ${name} returned ${response.status}: ${raw}`);
    }
    const envelope = raw.trim() === "" ? {} : JSON.parse(raw);
    if (envelope.error) {
      throw new Error(`MCP ${name} failed: ${JSON.stringify(envelope.error)}`);
    }
    const result = envelope.result || {};
    if (result.structuredContent !== undefined) {
      return result.structuredContent;
    }
    const content = Array.isArray(result.content) ? result.content : [];
    const text = content.find((item) => item && item.type === "text" && typeof item.text === "string");
    if (!text) {
      return result;
    }
    return JSON.parse(text.text);
  };
}
