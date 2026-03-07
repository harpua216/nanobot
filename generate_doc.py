from docx import Document
from docx.shared import Pt, RGBColor, Inches, Emu
from docx.enum.text import WD_ALIGN_PARAGRAPH
from docx.enum.table import WD_TABLE_ALIGNMENT
from docx.oxml.ns import qn
from docx.oxml import OxmlElement
import copy

doc = Document()

# ── Styles ────────────────────────────────────────────────────────────────────
def h1(text):
    p = doc.add_heading(text, level=1)
    p.runs[0].font.color.rgb = RGBColor(0x1A, 0x56, 0xDB)
    return p

def h2(text):
    p = doc.add_heading(text, level=2)
    p.runs[0].font.color.rgb = RGBColor(0x1E, 0x3A, 0x8A)
    return p

def h3(text):
    return doc.add_heading(text, level=3)

def para(text, bold=False):
    p = doc.add_paragraph(text)
    if bold:
        for run in p.runs:
            run.bold = True
    return p

def bullet(text, level=0):
    p = doc.add_paragraph(text, style='List Bullet')
    p.paragraph_format.left_indent = Inches(0.25 * (level + 1))
    return p

def code_block(text):
    p = doc.add_paragraph(text)
    p.style = doc.styles['Normal']
    for run in p.runs:
        run.font.name = 'Courier New'
        run.font.size = Pt(9)
        run.font.color.rgb = RGBColor(0x1F, 0x29, 0x37)
    # shading
    pPr = p._p.get_or_add_pPr()
    shd = OxmlElement('w:shd')
    shd.set(qn('w:val'), 'clear')
    shd.set(qn('w:color'), 'auto')
    shd.set(qn('w:fill'), 'F3F4F6')
    pPr.append(shd)
    return p

def divider():
    doc.add_paragraph('─' * 80)

def ascii_diagram(lines):
    for line in lines:
        p = doc.add_paragraph(line)
        for run in p.runs:
            run.font.name = 'Courier New'
            run.font.size = Pt(9)
        p.paragraph_format.space_after = Pt(0)

# ── Title Page ────────────────────────────────────────────────────────────────
title = doc.add_heading('NANOBOT', 0)
title.alignment = WD_ALIGN_PARAGRAPH.CENTER
title.runs[0].font.color.rgb = RGBColor(0x1A, 0x56, 0xDB)
title.runs[0].font.size = Pt(36)

sub = doc.add_paragraph('Build MCP Agents — Architecture & Vision Overview')
sub.alignment = WD_ALIGN_PARAGRAPH.CENTER
sub.runs[0].font.size = Pt(14)
sub.runs[0].font.color.rgb = RGBColor(0x6B, 0x72, 0x80)

doc.add_paragraph('March 2026  |  nanobot.ai').alignment == WD_ALIGN_PARAGRAPH.CENTER
doc.add_page_break()

# ══════════════════════════════════════════════════════════════════════════════
# 1. VISION & PURPOSE
# ══════════════════════════════════════════════════════════════════════════════
h1('1. Vision & Purpose')

para(
    'Nanobot is a standalone, open-source MCP (Model Context Protocol) host that '
    'makes it easy for anyone to build and deploy AI agents. Rather than relying on '
    'MCP hosts embedded inside closed products (VSCode, Claude Desktop, ChatGPT, Cursor), '
    'Nanobot gives developers full ownership of the hosting layer — combining MCP servers '
    'with LLMs and exposing the result through any interface they choose.'
)

h2('1.1  The Problem Being Solved')
bullet('MCP hosts today are embedded inside proprietary tools — you cannot deploy or customise them.')
bullet('Teams that want to ship agent experiences (chatbots, voice assistants, Slack bots, SMS) have no open, self-hostable foundation.')
bullet('There is no standard way to wire multiple MCP servers + multiple LLM providers + auth + UI into one deployable unit.')

h2('1.2  The Solution')
bullet('Nanobot is that deployable unit — a single binary (Go) + embedded UI (Svelte).')
bullet('Configure via YAML or Markdown files; run with one command.')
bullet('Supports OpenAI & Anthropic today; designed for easy provider expansion.')
bullet('Runs locally, on a server, or in Docker/Kubernetes.')

# ══════════════════════════════════════════════════════════════════════════════
# 2. WHAT IS AN MCP HOST
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('2. What is an MCP Host?')

para(
    'The Model Context Protocol (MCP) defines three roles. Nanobot fills the HOST role:'
)

ascii_diagram([
    '  ┌──────────────────────────────────────────────────────────────────┐',
    '  │                        MCP ECOSYSTEM                            │',
    '  │                                                                  │',
    '  │   ┌──────────────┐      ┌──────────────┐      ┌─────────────┐  │',
    '  │   │  MCP CLIENT  │◄────►│   MCP HOST   │◄────►│ MCP SERVER  │  │',
    '  │   │  (Consumer)  │      │  (Nanobot)   │      │ (Tool/Data) │  │',
    '  │   └──────────────┘      └──────┬───────┘      └─────────────┘  │',
    '  │                                │                                 │',
    '  │                         ┌──────▼───────┐                        │',
    '  │                         │  LLM Provider│                        │',
    '  │                         │ OpenAI/Anthro│                        │',
    '  │                         └──────────────┘                        │',
    '  └──────────────────────────────────────────────────────────────────┘',
])

doc.add_paragraph('')
bullet('MCP Server — exposes tools, prompts, resources (e.g. a Shopify API wrapper).')
bullet('MCP Host (Nanobot) — orchestrates servers + LLM; manages sessions, auth, routing.')
bullet('MCP Client — the end-user interface (chat UI, voice, SMS, Slack, another MCP client).')

# ══════════════════════════════════════════════════════════════════════════════
# 3. ARCHITECTURE
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('3. System Architecture')

h2('3.1  High-Level Component Map')

ascii_diagram([
    '  ┌────────────────────────────────────────────────────────────────────────┐',
    '  │                          NANOBOT BINARY                               │',
    '  │                                                                        │',
    '  │  ┌──────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │',
    '  │  │  CLI/cmd │  │  HTTP Server │  │   Runtime    │  │  Config      │  │',
    '  │  │ pkg/cmd  │  │  pkg/server  │  │  pkg/runtime │  │  pkg/config  │  │',
    '  │  └────┬─────┘  └──────┬───────┘  └──────┬───────┘  └──────────────┘  │',
    '  │       │               │                  │                             │',
    '  │       └───────────────▼──────────────────▼─────────────┐              │',
    '  │                       │        CORE SERVICES            │              │',
    '  │                       │                                 │              │',
    '  │  ┌────────────────────▼───┐  ┌──────────────────────┐  │              │',
    '  │  │   Agents (pkg/agents) │  │  Tools (pkg/tools)   │  │              │',
    '  │  │  - Tool mapping       │  │  - Tool registry     │  │              │',
    '  │  │  - LLM orchestration  │  │  - MCP connections   │  │              │',
    '  │  │  - Hooks              │  │  - Tool delegation   │  │              │',
    '  │  └──────────┬────────────┘  └──────────┬───────────┘  │              │',
    '  │             │                           │              │              │',
    '  │  ┌──────────▼───────────────────────────▼──────────┐  │              │',
    '  │  │               MCP Layer (pkg/mcp)                │  │              │',
    '  │  │  Session | Client | Server | Wire (stdio/HTTP)   │  │              │',
    '  │  └──────────┬──────────────────────────────────────-┘  │              │',
    '  │             │                                           │              │',
    '  │  ┌──────────▼──────────┐  ┌───────────────────────┐   │              │',
    '  │  │  LLM (pkg/llm)      │  │  Sessions (pkg/session)│   │              │',
    '  │  │  OpenAI | Anthropic │  │  State | OAuth | DB    │   │              │',
    '  │  └─────────────────────┘  └───────────────────────┘   │              │',
    '  │                                                         │              │',
    '  │  Built-in MCP Servers (pkg/servers/)                   │              │',
    '  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐  │              │',
    '  │  │  agent/  │ │  meta/   │ │resources/│ │workspace/│  │              │',
    '  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘  │              │',
    '  └────────────────────────────────────────────────────────────────────────┘',
    '',
    '  ┌──────────────────┐        ┌─────────────────────────────────────────┐',
    '  │  Embedded UI      │        │  External MCP Servers (user-defined)    │',
    '  │  Svelte 5 +       │◄──────►│  stdio, HTTP, Docker-sandboxed          │',
    '  │  SvelteKit        │        └─────────────────────────────────────────┘',
    '  └──────────────────┘',
])

doc.add_paragraph('')

h2('3.2  Backend Packages')

tbl = doc.add_table(rows=1, cols=2)
tbl.style = 'Table Grid'
hdr = tbl.rows[0].cells
hdr[0].text = 'Package'
hdr[1].text = 'Responsibility'
for cell in hdr:
    cell.paragraphs[0].runs[0].bold = True

rows = [
    ('pkg/runtime', 'Top-level wiring: initialises LLM, tools, agents, sampling'),
    ('pkg/agents', 'Agent execution engine; tool mapping, LLM calls, hook dispatch'),
    ('pkg/tools', 'Central tool registry; discovers and delegates to MCP servers'),
    ('pkg/mcp', 'MCP protocol: Session, Client, Server, Wire transports (stdio/HTTP)'),
    ('pkg/llm', 'LLM provider abstraction (OpenAI, Anthropic); completion & response APIs'),
    ('pkg/server', 'HTTP server; routes MCP-over-HTTP, session creation, request routing'),
    ('pkg/session / sessiondata', 'Session lifecycle, conversation state, OAuth tokens, parent-child sessions'),
    ('pkg/config', 'YAML config loading, schema validation, profiles, inheritance (extends)'),
    ('pkg/servers/agent', 'Built-in: exposes agents as MCP servers with chat capabilities'),
    ('pkg/servers/meta', 'Built-in: list_chats, update_chat, list_agents introspection tools'),
    ('pkg/servers/resources', 'Built-in: DB-backed resource management with mimetype detection'),
    ('pkg/servers/workspace', 'Built-in: workspace & session management'),
    ('pkg/servers/capabilities', 'Built-in: session initialisation & capability negotiation'),
    ('pkg/sampling', 'Handles MCP sampling (LLM-in-the-loop for MCP servers)'),
    ('pkg/mcp/sandbox', 'Docker containerisation & port mapping for sandboxed MCP servers'),
    ('pkg/types', 'Shared type definitions: Config, Agent, Message, ToolCall, etc.'),
    ('pkg/expr', 'Dynamic expression evaluation in configuration values'),
    ('pkg/supervise', 'Process supervision for MCP server subprocesses'),
]
for pkg, desc in rows:
    row = tbl.add_row().cells
    row[0].text = pkg
    row[1].text = desc

doc.add_paragraph('')

h2('3.3  Frontend Architecture')
bullet('Framework: Svelte 5 (runes-based reactivity) + SvelteKit (static adapter)')
bullet('Styling: TailwindCSS 4 + DaisyUI component library')
bullet('Icons: Lucide (@lucide/svelte)')
bullet('Package manager: pnpm')
bullet('Key file — src/lib/chat.svelte.ts: core chat state & API using Svelte 5 runes')
bullet('Communication: HTTP to /mcp/ui (MCP-UI protocol) with event-streaming for real-time updates')
bullet('Session identity: Mcp-Session-Id header')
bullet('Dev workflow: backend on :8080 proxies UI requests to Vite dev server on :5173')

# ══════════════════════════════════════════════════════════════════════════════
# 4. REQUEST FLOW
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('4. Request & Agent Execution Flow')

ascii_diagram([
    '  User / Client Interface',
    '        │  (HTTP / MCP-UI / stdio)',
    '        ▼',
    '  ┌─────────────────────────┐',
    '  │   pkg/server            │  ← routes initialize, tools/list, tools/call,',
    '  │   HTTP Server           │    prompts/*, resources/*',
    '  └──────────┬──────────────┘',
    '             │',
    '             ▼',
    '  ┌─────────────────────────┐',
    '  │   pkg/session           │  ← creates / resumes session',
    '  │   Session Manager       │    loads tool mappings & agent context',
    '  └──────────┬──────────────┘',
    '             │',
    '             ▼',
    '  ┌─────────────────────────┐',
    '  │   pkg/agents            │  ← resolves agent, applies hooks',
    '  │   Agent Engine          │    builds system prompt + tool list',
    '  └──────────┬──────────────┘',
    '             │',
    '     ┌───────▼────────┐',
    '     │  pkg/llm       │  ← sends request to OpenAI or Anthropic',
    '     │  LLM Provider  │',
    '     └───────┬────────┘',
    '             │  (tool_use response)',
    '             ▼',
    '  ┌─────────────────────────┐',
    '  │   pkg/tools             │  ← looks up tool in registry',
    '  │   Tool Service          │    delegates to correct MCP server',
    '  └──────────┬──────────────┘',
    '             │',
    '             ▼',
    '  ┌─────────────────────────┐',
    '  │   External / Built-in   │  ← executes tool, returns result',
    '  │   MCP Server            │',
    '  └──────────┬──────────────┘',
    '             │  (tool result)',
    '             └──────────────► back to Agent Engine → LLM → final response',
])

doc.add_paragraph('')

# ══════════════════════════════════════════════════════════════════════════════
# 5. CONFIGURATION
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('5. Configuration Model')

h2('5.1  Two Configuration Formats')

h3('Single-File (nanobot.yaml)')
code_block(
    'agents:\n'
    '  dealer:\n'
    '    name: Blackjack Dealer\n'
    '    model: gpt-4.1\n'
    '    mcpServers: blackjackmcp\n'
    '\n'
    'mcpServers:\n'
    '  blackjackmcp:\n'
    '    url: https://blackjack.nanobot.ai/mcp'
)

h3('Directory-Based (recommended for larger projects)')
code_block(
    'my-config/\n'
    '├── agents/\n'
    '│   ├── main.md       ← auto-detected entrypoint\n'
    '│   └── helper.md\n'
    '└── mcp-servers.yaml'
)
para('Each agent .md file uses YAML front-matter for settings; the body becomes the system prompt.')

h2('5.2  Key Configuration Sections')

tbl2 = doc.add_table(rows=1, cols=2)
tbl2.style = 'Table Grid'
hdr2 = tbl2.rows[0].cells
hdr2[0].text = 'Section'
hdr2[1].text = 'Purpose'
for cell in hdr2:
    cell.paragraphs[0].runs[0].bold = True

cfg_rows = [
    ('agents', 'Define agents: model, instructions, tools, temperature, etc.'),
    ('mcpServers', 'MCP server configs: command, URL, Docker image, headers'),
    ('prompts', 'Reusable prompt templates'),
    ('publish', 'What to expose when Nanobot itself acts as an MCP server'),
    ('env', 'Environment variable definitions with defaults'),
    ('auth', 'OAuth and remote header authentication'),
    ('profiles', 'Configuration profiles for different environments'),
    ('extends', 'Inherit from another configuration file'),
]
for section, purpose in cfg_rows:
    row = tbl2.add_row().cells
    row[0].text = section
    row[1].text = purpose

doc.add_paragraph('')

# ══════════════════════════════════════════════════════════════════════════════
# 6. KEY PATTERNS
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('6. Key Architectural Patterns')

h2('6.1  Tool Mappings')
para(
    'The BuildToolMappings method in pkg/agents/ resolves which tools each agent can '
    'access by traversing the MCP server graph and creating a flat mapping of '
    'tool-name → MCP server + method. This mapping is scoped to the session.'
)

h2('6.2  Hooks (Lifecycle Extensibility)')
ascii_diagram([
    '  Agent Lifecycle',
    '  ┌──────────┐   config hook   ┌──────────┐   request hook  ┌──────────┐',
    '  │  Config  │ ──────────────► │  Setup   │ ──────────────► │  LLM     │',
    '  │  Load    │                 │  Agent   │                 │  Call    │',
    '  └──────────┘                 └──────────┘                 └────┬─────┘',
    '                                                                  │',
    '                                                      response hook',
    '                                                                  ▼',
    '                                                         ┌──────────────┐',
    '                                                         │  Post-process│',
    '                                                         │  Response    │',
    '                                                         └──────────────┘',
])
doc.add_paragraph('')
bullet('Hooks are TypeScript/JavaScript functions executed by goja (embedded JS runtime).')
bullet('They can mutate configuration, request messages, and response messages.')
bullet('Defined in hooks.ts (types) and pkg/types/hooks.go (Go types).')

h2('6.3  Sandboxing')
bullet('MCP servers can run inside Docker containers for process isolation.')
bullet('pkg/mcp/sandbox/ manages container lifecycle and port mapping.')
bullet('Enables untrusted or third-party MCP servers to be run safely.')

h2('6.4  Multi-Agent Support')
bullet('Agents can be exposed as MCP servers themselves (pkg/servers/agent/).')
bullet('One agent can call another as a tool — enabling agent chains/hierarchies.')
bullet('Sessions support parent-child relationships for scoped state.')

h2('6.5  Special _exec Mode')
para(
    'When invoked as nanobot _exec ..., Nanobot acts as a daemon wrapper for an MCP '
    'server subprocess — handling stdio piping and process lifecycle. This lets Nanobot '
    'supervise external MCP servers.'
)

# ══════════════════════════════════════════════════════════════════════════════
# 7. TECH STACK SUMMARY
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('7. Technology Stack')

tbl3 = doc.add_table(rows=1, cols=3)
tbl3.style = 'Table Grid'
hdr3 = tbl3.rows[0].cells
hdr3[0].text = 'Layer'
hdr3[1].text = 'Technology'
hdr3[2].text = 'Notes'
for cell in hdr3:
    cell.paragraphs[0].runs[0].bold = True

stack = [
    ('Backend language', 'Go 1.26', 'Single compiled binary'),
    ('ORM / DB', 'GORM', 'SQLite (default), MySQL, PostgreSQL'),
    ('JS Runtime (hooks)', 'goja', 'Embedded V8-compatible JS engine'),
    ('Frontend framework', 'Svelte 5 + SvelteKit', 'Static adapter; runes-based reactivity'),
    ('Styling', 'TailwindCSS 4 + DaisyUI', 'Utility-first CSS'),
    ('Icons', 'Lucide (@lucide/svelte)', 'Consistent icon library'),
    ('Frontend pkg mgr', 'pnpm', 'Fast, disk-efficient'),
    ('LLM Providers', 'OpenAI, Anthropic', 'Auto-selected by model name prefix'),
    ('MCP Transport', 'stdio, HTTP', 'Both standard MCP transports'),
    ('Sandboxing', 'Docker', 'Optional containerisation of MCP servers'),
    ('Auth', 'OAuth, Remote Headers', 'Pluggable auth layer'),
    ('Installation', 'Homebrew / make', 'nanobot run ./config.yaml'),
]
for layer, tech, notes in stack:
    row = tbl3.add_row().cells
    row[0].text = layer
    row[1].text = tech
    row[2].text = notes

doc.add_paragraph('')

# ══════════════════════════════════════════════════════════════════════════════
# 8. INTERFACES SUPPORTED
# ══════════════════════════════════════════════════════════════════════════════
h1('8. Supported & Planned Interfaces')

ascii_diagram([
    '                        ┌─────────────┐',
    '                        │   NANOBOT   │',
    '                        │    HOST     │',
    '                        └──────┬──────┘',
    '           ┌────────────┬──────┴──────┬────────────┬────────────┐',
    '           ▼            ▼             ▼            ▼            ▼',
    '      ┌────────┐  ┌──────────┐  ┌─────────┐  ┌────────┐  ┌──────────┐',
    '      │  Chat  │  │  Voice   │  │   SMS   │  │  Slack │  │  MCP     │',
    '      │  Web   │  │ (planned)│  │(planned)│  │(planned│  │ Client   │',
    '      │   UI   │  └──────────┘  └─────────┘  └────────┘  └──────────┘',
    '      └────────┘',
    '           ▼',
    '      ┌────────┐',
    '      │ Email /│',
    '      │AR/VR   │',
    '      │(future)│',
    '      └────────┘',
])

doc.add_paragraph('')

# ══════════════════════════════════════════════════════════════════════════════
# 9. ROADMAP
# ══════════════════════════════════════════════════════════════════════════════
h1('9. Roadmap')

bullet('Full MCP + MCP-UI specification compliance')
bullet('More robust multi-agent orchestration')
bullet('Production-ready UI with advanced chat features')
bullet('Expanded LLM provider support (beyond OpenAI + Anthropic)')
bullet('Expanded auth & security features')
bullet('Frontend integrations: Slack, SMS, email, embedded web agents')
bullet('Easy embedding into existing apps and websites')

# ══════════════════════════════════════════════════════════════════════════════
# 10. LIVE EXAMPLES
# ══════════════════════════════════════════════════════════════════════════════
doc.add_page_break()
h1('10. Live Examples')

tbl4 = doc.add_table(rows=1, cols=3)
tbl4.style = 'Table Grid'
hdr4 = tbl4.rows[0].cells
hdr4[0].text = 'Demo'
hdr4[1].text = 'URL'
hdr4[2].text = 'Description'
for cell in hdr4:
    cell.paragraphs[0].runs[0].bold = True

examples = [
    ('Blackjack Game', 'blackjack.nanobot.ai', 'Full card game powered by a Nanobot agent'),
    ('Hugging Face MCP', 'huggingface.nanobot.ai', 'AI model discovery via Hugging Face MCP'),
    ('Shopping / Shopify', 'shopping.nanobot.ai', 'E-commerce shopping assistant'),
]
for name, url, desc in examples:
    row = tbl4.add_row().cells
    row[0].text = name
    row[1].text = url
    row[2].text = desc

doc.add_paragraph('')

# ══════════════════════════════════════════════════════════════════════════════
# Footer note
# ══════════════════════════════════════════════════════════════════════════════
divider()
para(
    'Nanobot is alpha software under active development. '
    'Expect breaking changes as the architecture evolves. '
    'License: Apache 2.0  |  github.com/nanobot-ai/nanobot  |  nanobot.ai',
    bold=False
)

# ── Save ──────────────────────────────────────────────────────────────────────
out = '/home/user/nanobot/Nanobot_Architecture_Overview.docx'
doc.save(out)
print(f'Saved: {out}')
