// Démarre bambud (ou utilise BAMBUD_URL) puis valide tools/list avec ListToolsResultSchema
// du SDK MCP TypeScript : c'est exactement la validation qui fait échouer Claude Desktop.
// Usage : node validate.mjs            (go run ./cmd/bambud sur un port libre)
//         BAMBUD_BIN=/chemin/bambud node validate.mjs
//         BAMBUD_URL=http://127.0.0.1:7878/mcp node validate.mjs   (instance déjà lancée)
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { ListToolsResultSchema } from "@modelcontextprotocol/sdk/types.js";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

function freePort() {
  return new Promise((res, rej) => {
    const s = createServer();
    s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); });
    s.on("error", rej);
  });
}

async function waitReady(url, child) {
  for (let i = 0; i < 240; i++) {
    if (child && child.exitCode !== null) throw new Error("bambud s'est arrêté (code " + child.exitCode + ")");
    try { const r = await fetch(url.replace(/\/mcp$/, "/health")); if (r.ok) return; } catch {}
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("bambud ne répond pas");
}

let child, dir, url = process.env.BAMBUD_URL;
try {
  if (!url) {
    const port = await freePort();
    dir = mkdtempSync(join(tmpdir(), "bambud-validate-"));
    const args = ["-addr", `127.0.0.1:${port}`, "-data-dir", dir];
    child = process.env.BAMBUD_BIN
      ? spawn(process.env.BAMBUD_BIN, args, { cwd: root, stdio: "inherit" })
      : spawn("go", ["run", "./cmd/bambud", ...args], { cwd: root, stdio: "inherit" });
    url = `http://127.0.0.1:${port}/mcp`;
  }
  await waitReady(url, child);

  // 1) Réponse tools/list brute, validée par le schéma zod strict du SDK.
  const client = new Client({ name: "validate", version: "0" });
  await client.connect(new StreamableHTTPClientTransport(new URL(url)));
  const raw = await client.request({ method: "tools/list", params: {} }, ListToolsResultSchema);
  const bad = [];
  for (const t of raw.tools) {
    if (t.inputSchema?.type !== "object") bad.push(`${t.name}: inputSchema.type=${JSON.stringify(t.inputSchema?.type)}`);
    if (t.outputSchema && t.outputSchema.type !== "object") bad.push(`${t.name}: outputSchema.type=${JSON.stringify(t.outputSchema.type)}`);
  }
  // 2) Chemin utilisé par les vrais clients : listTools() valide aussi les outputSchema.
  const listed = await client.listTools();
  await client.close();
  if (bad.length) throw new Error("schémas non conformes :\n" + bad.join("\n"));
  console.log(`OK : ${raw.tools.length} outils validés par ListToolsResultSchema (SDK TS), ${listed.tools.length} via Client.listTools()`);
} catch (e) {
  console.error("ÉCHEC :", e?.message ?? e);
  process.exitCode = 1;
} finally {
  if (child) child.kill();
  if (dir) { await new Promise((r) => setTimeout(r, 500)); try { rmSync(dir, { recursive: true, force: true }); } catch {} }
}
