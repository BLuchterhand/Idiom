import { useEffect, useMemo, useState } from "react";
import type { CommandRequest, DesktopManifest, IdiomBridge, IdiomEvent, LaneKind } from "../core/idiom";
import { eventFromText } from "../core/idiom";
import { createBridge } from "../adapters/createBridge";

const lanes: LaneKind[] = ["plan-execute", "implement", "atom-spec"];

type SurfaceKey = "runs" | "memory" | "audit" | "recover" | "settings";

interface Surface {
  key: SurfaceKey;
  label: string;
  shortcut: string;
  description: string;
}

interface QuickAction {
  label: string;
  description: string;
  request: CommandRequest;
}

const surfaces: Surface[] = [
  {
    key: "runs",
    label: "Runs",
    shortcut: "R",
    description: "Start lanes, watch event streams, and keep terminal parity with plan-execute, implement, and atom-spec.",
  },
  {
    key: "memory",
    label: "Memory",
    shortcut: "M",
    description: "Inspect repository memory, tool schemas, discovery profile tools, and context freshness.",
  },
  {
    key: "audit",
    label: "Audit",
    shortcut: "A",
    description: "Run harness audits and surface evidence trails without leaving the desktop app.",
  },
  {
    key: "recover",
    label: "Recover",
    shortcut: "V",
    description: "Reach product workflow and recovery status surfaces that mirror the current CLI/TUI operators.",
  },
  {
    key: "settings",
    label: "Settings",
    shortcut: "S",
    description: "Check backend, queue, profile, skills, and binary status.",
  },
];

const quickActionsBySurface: Record<SurfaceKey, QuickAction[]> = {
  runs: [
    { label: "Doctor", description: "Check host readiness profiles.", request: { command: "doctor", args: ["--json"] } },
    { label: "Backend status", description: "Inspect planner/executor backend state.", request: { command: "backend", args: ["status"] } },
    { label: "Queue status", description: "Show durable runner queue state.", request: { command: "queue", args: ["status", "--json"] } },
  ],
  memory: [
    { label: "Context status", description: "Check graph freshness through sidecar tooling.", request: { command: "tool", args: ["context_status", "--profile", "discovery", "--input", "{\"refresh\":false,\"limit\":20}"] } },
    { label: "Tool list", description: "List discovery-safe tools.", request: { command: "tool", args: ["list", "--profile", "discovery"] } },
    { label: "Tool schemas", description: "Export the sidecar schema surface.", request: { command: "tool", args: ["schemas"] } },
  ],
  audit: [
    { label: "Harness audit", description: "Run behavior audit through the tool sidecar.", request: { command: "tool", args: ["harness_audit", "--profile", "discovery", "--input", "{\"purpose\":\"behavior\"}"] } },
    { label: "Audit help", description: "Show audit command surface.", request: { command: "audit", args: ["--help"] } },
    { label: "Probe lane", description: "Check lane route wiring.", request: { command: "probe", args: ["lane", "--mode", "all"] } },
  ],
  recover: [
    { label: "Recover help", description: "Show inspect/status/decompose/promote/reconcile commands.", request: { command: "recover", args: ["--help"] } },
    { label: "Product efforts", description: "List product workflow efforts.", request: { command: "product", args: ["efforts", "--json"] } },
    { label: "Product help", description: "Show PRD/ADR/TDD/brief workflow commands.", request: { command: "product", args: ["--help"] } },
  ],
  settings: [
    { label: "Version", description: "Report the current idiom binary version.", request: { command: "version", args: [] } },
    { label: "Profiles", description: "Inspect profile command surface.", request: { command: "profile", args: ["--help"] } },
    { label: "Skills", description: "Inspect skills command surface.", request: { command: "skills", args: ["--help"] } },
  ],
};

const startingEvents: IdiomEvent[] = [
  eventFromText({
    type: "surface",
    level: "info",
    title: "Desktop surface ready",
    detail: "A dedicated product shell for contracts, repository memory, lanes, audit, and evals.",
  }),
  eventFromText({
    type: "hook",
    level: "success",
    title: "Bridge abstraction online",
    detail: "The UI is connected through IdiomBridge, so sidecar, MCP, or socket transports can replace the adapter.",
  }),
];

export function App() {
  const bridge = useMemo<IdiomBridge>(() => createBridge(), []);
  const [manifest, setManifest] = useState<DesktopManifest | null>(null);
  const [events, setEvents] = useState<IdiomEvent[]>(startingEvents);
  const [lane, setLane] = useState<LaneKind>("plan-execute");
  const [prompt, setPrompt] = useState("Inspect the current workspace and propose the smallest safe next change.");
  const [workspace, setWorkspace] = useState("");
  const [activeSurface, setActiveSurface] = useState<SurfaceKey>("runs");
  const [commandOutput, setCommandOutput] = useState("No command output yet.");
  const [running, setRunning] = useState(false);

  useEffect(() => {
    let mounted = true;
    let unsubscribe: (() => void) | undefined;

    bridge.appManifest()
      .then((value) => {
        if (!mounted) {
          return;
        }

        setManifest(value);
        setWorkspace(value.workspace);
      })
      .catch((error: unknown) => {
        setEvents((current) => [eventFromText({
          type: "error",
          level: "error",
          title: "Unable to read app manifest",
          detail: error instanceof Error ? error.message : String(error),
        }), ...current]);
      });

    bridge.subscribe((event) => {
      setEvents((current) => [event, ...current].slice(0, 80));
      if (event.type === "lane_completed" || event.type === "lane_failed" || event.type === "lane_exit") {
        setRunning(false);
      }
    }).then((value) => {
      unsubscribe = value;
    });

    return () => {
      mounted = false;
      unsubscribe?.();
    };
  }, [bridge]);

  async function startLane() {
    setRunning(true);
    try {
      await bridge.startLane({ lane, prompt, workspace });
    } catch (error) {
      setRunning(false);
      setEvents((current) => [eventFromText({
        type: "lane_failed",
        level: "error",
        title: "Lane failed to start",
        detail: error instanceof Error ? error.message : String(error),
      }), ...current]);
    }
  }

  async function checkMemory() {
    const result = await bridge.runTool({
      tool: "context_status",
      workspace,
      input: { refresh: false, limit: 20 },
    });

    setEvents((current) => [eventFromText({
      type: "tool_result",
      level: result.status === 0 ? "success" : "warning",
      title: "context_status",
      detail: result.stdout || result.stderr,
      payload: result,
    }), ...current].slice(0, 80));
  }

  async function runQuickAction(action: QuickAction) {
    setRunning(true);
    try {
      const result = await bridge.runCommand({ ...action.request, workspace });
      const detail = result.stdout || result.stderr || `exit status ${result.status}`;

      setCommandOutput(detail);
      setEvents((current) => [eventFromText({
        type: "command_result",
        level: result.status === 0 ? "success" : "warning",
        title: `idiom ${action.request.command}`,
        detail,
        payload: result,
      }), ...current].slice(0, 80));
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);

      setCommandOutput(detail);
      setEvents((current) => [eventFromText({
        type: "command_failed",
        level: "error",
        title: `idiom ${action.request.command}`,
        detail,
      }), ...current].slice(0, 80));
    } finally {
      setRunning(false);
    }
  }

  const latestRun = events.find((event) => event.sessionId);
  const failures = events.filter((event) => event.level === "error").length;
  const evidenceCount = events.filter((event) => ["tool", "tool_result", "memory", "contract"].includes(event.type)).length;
  const activeSurfaceDetails = surfaces.find((surface) => surface.key === activeSurface) ?? surfaces[0];
  const quickActions = quickActionsBySurface[activeSurface];

  return (
    <main className="shell">
      <aside className="rail" aria-label="Idiom surfaces">
        <div className="brand-mark">id</div>
        {surfaces.map((surface) => (
          <button
            key={surface.key}
            className={`rail-button ${activeSurface === surface.key ? "active" : ""}`}
            aria-label={surface.label}
            onClick={() => setActiveSurface(surface.key)}
          >
            {surface.shortcut}
          </button>
        ))}
      </aside>

      <section className="workspace">
        <header className="topbar">
          <div>
            <p className="eyebrow">Local agent substrate</p>
            <h1>Idiom</h1>
          </div>
          <div className="runtime-card" aria-label="Runtime status">
            <span className="status-dot" />
            <div>
              <strong>{manifest?.version ?? "loading"}</strong>
              <span>{manifest?.idiomBinary ?? "resolving idiom binary"}</span>
            </div>
          </div>
        </header>

        <section className="control-strip" aria-label="Run controls">
          <label>
            Lane
            <select value={lane} onChange={(event) => setLane(event.target.value as LaneKind)}>
              {lanes.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
          <label className="workspace-field">
            Workspace
            <input value={workspace} onChange={(event) => setWorkspace(event.target.value)} />
          </label>
          <button onClick={checkMemory}>Check memory</button>
        </section>

        <section className="main-grid">
          <section className="composer" aria-label="Prompt composer">
            <div className="panel-heading">
              <span>New run</span>
              <small>{latestRun?.sessionId ? `session ${latestRun.sessionId.slice(0, 8)}` : "no active session"}</small>
            </div>
            <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} />
            <div className="composer-actions">
              <span>Contracts stay host-owned. The model still chooses strategy.</span>
              <button className="primary" onClick={startLane} disabled={running || prompt.trim() === ""}>
                {running ? "Running" : "Start lane"}
              </button>
            </div>
          </section>

          <section className="metrics" aria-label="Run health">
            <Metric label="Evidence" value={String(evidenceCount)} tone="blue" />
            <Metric label="Open failures" value={String(failures)} tone={failures > 0 ? "red" : "green"} />
            <Metric label="Events" value={String(events.length)} tone="violet" />
          </section>

          <section className="surface-panel" aria-label={`${activeSurfaceDetails.label} controls`}>
            <div className="panel-heading">
              <span>{activeSurfaceDetails.label}</span>
              <small>{activeSurfaceDetails.shortcut}</small>
            </div>
            <p>{activeSurfaceDetails.description}</p>
            <div className="quick-actions">
              {quickActions.map((action) => (
                <button key={action.label} onClick={() => runQuickAction(action)} disabled={running}>
                  <strong>{action.label}</strong>
                  <span>{action.description}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="timeline" aria-label="Run timeline">
            <div className="panel-heading">
              <span>Run timeline</span>
              <small>contracts, memory, tools, audit</small>
            </div>
            <div className="event-list">
              {events.map((event) => <EventRow key={event.id} event={event} />)}
            </div>
          </section>

          <section className="command-output" aria-label="Command output">
            <div className="panel-heading">
              <span>Command output</span>
              <small>raw CLI parity</small>
            </div>
            <pre>{commandOutput}</pre>
          </section>

          <section className="substrate" aria-label="Idiom substrate">
            <div className="panel-heading">
              <span>Substrate</span>
              <small>what Idiom exposes beyond chat</small>
            </div>
            <div className="substrate-grid">
              <SubstrateItem title="Repo memory" body="Fresh context packets, symbols, traces, and impact views." />
              <SubstrateItem title="Contracts" body="Permissions, path roles, obligations, verification, and fuses." />
              <SubstrateItem title="Audit" body="Structured event trails for replay, evals, and regression protection." />
              <SubstrateItem title="Adapters" body="Bridge hooks for sidecar commands, MCP, sockets, or hosted control planes." />
            </div>
          </section>
        </section>
      </section>
    </main>
  );
}

function Metric({ label, value, tone }: { label: string; value: string; tone: "blue" | "green" | "red" | "violet" }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function EventRow({ event }: { event: IdiomEvent }) {
  return (
    <article className={`event ${event.level}`}>
      <div className="event-glyph">{event.type.slice(0, 1).toUpperCase()}</div>
      <div>
        <header>
          <strong>{event.title}</strong>
          <time>{formatEventTime(event.timestamp)}</time>
        </header>
        {event.detail ? <p>{event.detail}</p> : null}
      </div>
    </article>
  );
}

function formatEventTime(timestamp: string) {
  const numeric = Number(timestamp);
  const date = Number.isFinite(numeric) && timestamp.trim() !== ""
    ? new Date(numeric)
    : new Date(timestamp);

  if (Number.isNaN(date.getTime())) {
    return "now";
  }

  return date.toLocaleTimeString();
}

function SubstrateItem({ title, body }: { title: string; body: string }) {
  return (
    <article className="substrate-item">
      <strong>{title}</strong>
      <p>{body}</p>
    </article>
  );
}
