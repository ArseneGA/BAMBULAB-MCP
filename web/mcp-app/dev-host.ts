import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import { AppBridge, PostMessageTransport, getToolUiResourceUri, RESOURCE_MIME_TYPE } from '@modelcontextprotocol/ext-apps/app-bridge';

/**
 * Hôte MCP Apps simulé pour vérifier la vue sans Claude Desktop :
 *  1. client MCP réel vers bambud (/mcp, relayé par le serveur de développement) ;
 *  2. appel réel d'un outil (search_models, get_model_details) ;
 *  3. lecture réelle de la ressource ui:// liée par _meta.ui.resourceUri (contrôle du MIME) ;
 *  4. iframe sandboxée (allow-scripts allow-same-origin) + AppBridge : initialisation, ui/notifications/tool-result,
 *     proxy des tools/call de la vue (get_mesh_chunk), ui/update-model-context journalisé, thème d'hôte.
 */

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const log$ = $('log'), status$ = $('status');
const log = (cls: 'ok' | 'err' | '', msg: string) => {
  const line = document.createElement('div'); if (cls) line.className = cls;
  line.textContent = `${new Date().toLocaleTimeString('fr-FR')}  ${msg}`; log$.appendChild(line); log$.scrollTop = log$.scrollHeight;
};

let theme: 'light' | 'dark' = 'light';
let bridge: AppBridge | null = null;
let client: Client | null = null;

async function getClient(): Promise<Client> {
  if (client) return client;
  const c = new Client({ name: 'dev-host', version: '0.3.0' });
  await c.connect(new StreamableHTTPClientTransport(new URL('/mcp', location.origin)));
  log('ok', 'client MCP connecté à /mcp');
  client = c;
  return c;
}

async function run(name: string, args: Record<string, unknown>) {
  status$.textContent = 'en cours…';
  try {
    const c = await getClient();
    const tools = (await c.listTools()).tools;
    const tool = tools.find((t) => t.name === name);
    if (!tool) throw new Error(`outil ${name} introuvable`);
    const uri = getToolUiResourceUri(tool);
    log(uri ? 'ok' : 'err', `outil ${name} -> ressource ${uri ?? '(aucune)'}`);
    const hidden = tools.filter((t) => (t._meta as { ui?: { visibility?: string[] } } | undefined)?.ui?.visibility?.join() === 'app').map((t) => t.name);
    log('', `outils réservés à la vue (visibility=app) : ${hidden.join(', ') || 'aucun'}`);
    const result = await c.callTool({ name, arguments: args });
    log(result.isError ? 'err' : 'ok', `résultat : ${JSON.stringify(result.content).slice(0, 160)}`);
    if (!uri) throw new Error("pas de vue liée : repli texte uniquement");
    const rr = await c.readResource({ uri });
    const content = rr.contents[0] as { mimeType?: string; text?: string; _meta?: unknown };
    log(content.mimeType === RESOURCE_MIME_TYPE ? 'ok' : 'err', `MIME ${content.mimeType} (attendu ${RESOURCE_MIME_TYPE}) · ${(content.text ?? '').length} caractères · _meta ${JSON.stringify(content._meta)}`);
    if (!content.text) throw new Error('ressource vide');

    const frame = $('frame');
    frame.replaceChildren();
    const iframe = document.createElement('iframe');
    iframe.setAttribute('sandbox', 'allow-scripts allow-same-origin');
    iframe.title = 'Vue MCP App';
    frame.appendChild(iframe);
    iframe.srcdoc = content.text;

    bridge = new AppBridge(c, { name: 'dev-host', version: '0.3.0' },
      { openLinks: {}, serverTools: {}, serverResources: {}, updateModelContext: { text: {} } },
      { hostContext: { theme, displayMode: 'inline', availableDisplayModes: ['inline'], containerDimensions: { maxHeight: 600 } } });
    bridge.oninitialized = () => { log('ok', 'vue initialisée -> envoi de ui/notifications/tool-result'); void bridge!.sendToolResult(result as never); };
    bridge.onsizechange = ({ height }) => { if (height) iframe.style.height = `${Math.ceil(height)}px`; };
    bridge.onupdatemodelcontext = async (p) => { log('ok', `ui/update-model-context : ${JSON.stringify(p.content)}`); return {}; };
    bridge.onopenlink = async ({ url }) => { log('', `ui/open-link ${url} (ignoré)`); return {}; };
    bridge.onmessage = async (p) => { log('', `ui/message ${JSON.stringify(p)}`); return {}; };
    await bridge.connect(new PostMessageTransport(iframe.contentWindow!, iframe.contentWindow!));
    status$.textContent = 'vue montée';
  } catch (e) {
    log('err', (e as Error).message); status$.textContent = 'erreur';
  }
}

$('run').addEventListener('click', () => {
  let args: Record<string, unknown> = {};
  try { args = JSON.parse(($('args') as HTMLInputElement).value || '{}'); } catch { log('err', 'arguments JSON invalides'); return; }
  void run(($('tool') as HTMLSelectElement).value, args);
});
$('theme').addEventListener('click', () => {
  theme = theme === 'light' ? 'dark' : 'light';
  document.documentElement.dataset.theme = theme;
  $('theme').textContent = `Thème : ${theme === 'light' ? 'clair' : 'sombre'}`;
  bridge?.setHostContext({ theme, displayMode: 'inline' });
  log('', `contexte d'hôte : thème ${theme}`);
});

(window as unknown as { __host: unknown }).__host = { run };
