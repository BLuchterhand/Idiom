import { useEffect, useMemo, useState } from "react";
import type { CommandRequest, DesktopManifest, IdiomBridge, IdiomEvent, LaneKind } from "../core/idiom";
import { eventFromText } from "../core/idiom";
import { createBridge } from "../adapters/createBridge";

const lanes: LaneKind[] = ["plan-execute", "implement", "atom-spec"];
const sidebarTabs = ["chat", "plan", "memory", "product", "recover", "audit", "settings"] as const;
const productTransitionActions = [
  "submit_for_human_verification",
  "approve_for_implementation",
  "request_changes",
  "request_investigation",
  "start_investigation",
  "submit_for_verification",
  "submit_for_review",
  "approve",
  "complete_investigation",
  "mark_blocked",
  "resolve_blocker",
];

type SidebarTab = typeof sidebarTabs[number];
type ChatRole = "user" | "assistant" | "operator";

interface QuickAction {
  label: string;
  request: CommandRequest;
}

interface ChatMessage {
  id: string;
  role: ChatRole;
  title?: string;
  body: string;
  timestamp: string;
  level?: "info" | "success" | "warning" | "error";
}

const quickActionsByTab: Partial<Record<SidebarTab, QuickAction[]>> = {
  chat: [
    { label: "Doctor", request: { command: "doctor", args: ["--json"] } },
    { label: "Backend", request: { command: "backend", args: ["status"] } },
    { label: "Queue", request: { command: "queue", args: ["status", "--json"] } },
  ],
  plan: [
    { label: "Probe lanes", request: { command: "probe", args: ["lane", "--mode", "all"] } },
    { label: "Brief help", request: { command: "brief", args: ["--help"] } },
    { label: "Lane help", request: { command: "lane", args: ["--help"] } },
  ],
  memory: [
    { label: "Context status", request: { command: "tool", args: ["context_status", "--profile", "discovery", "--input", "{\"refresh\":false,\"limit\":20}"] } },
    { label: "Tool list", request: { command: "tool", args: ["list", "--profile", "discovery"] } },
    { label: "Schemas", request: { command: "tool", args: ["schemas"] } },
  ],
  audit: [
    { label: "Harness audit", request: { command: "tool", args: ["harness_audit", "--profile", "discovery", "--input", "{\"purpose\":\"behavior\"}"] } },
    { label: "Audit help", request: { command: "audit", args: ["--help"] } },
    { label: "Probe", request: { command: "probe", args: ["lane", "--mode", "all"] } },
  ],
  product: [
    { label: "Efforts", request: { command: "product", args: ["efforts", "--json"] } },
    { label: "Product help", request: { command: "product", args: ["--help"] } },
  ],
  recover: [
    { label: "Recover help", request: { command: "recover", args: ["--help"] } },
    { label: "Show failure", request: { command: "recover", args: ["show", "latest", "--json"] } },
  ],
  settings: [
    { label: "Version", request: { command: "version", args: [] } },
    { label: "Profiles", request: { command: "profile", args: ["--help"] } },
    { label: "Skills", request: { command: "skills", args: ["--help"] } },
  ],
};

const initialMessages: ChatMessage[] = [
  {
    id: crypto.randomUUID(),
    role: "assistant",
    title: "Idiom",
    body: "Ready.",
    timestamp: new Date().toISOString(),
    level: "success",
  },
];

export function App() {
  const bridge = useMemo<IdiomBridge>(() => createBridge(), []);
  const [manifest, setManifest] = useState<DesktopManifest | null>(null);
  const [events, setEvents] = useState<IdiomEvent[]>([]);
  const [messages, setMessages] = useState<ChatMessage[]>(initialMessages);
  const [lane, setLane] = useState<LaneKind>("plan-execute");
  const [prompt, setPrompt] = useState("Inspect the current workspace and propose the smallest safe next change.");
  const [workspace, setWorkspace] = useState("");
  const [activeTab, setActiveTab] = useState<SidebarTab>("chat");
  const [commandOutput, setCommandOutput] = useState("No command output yet.");
  const [running, setRunning] = useState(false);
  const [effortRoot, setEffortRoot] = useState("");
  const [transitionAction, setTransitionAction] = useState("submit_for_human_verification");
  const [recoveryPacket, setRecoveryPacket] = useState("");

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
        appendOperatorMessage("Manifest", error instanceof Error ? error.message : String(error), "error");
      });

    bridge.subscribe((event) => {
      setEvents((current) => [event, ...current].slice(0, 120));
      appendMessage(messageFromEvent(event));
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
    const body = prompt.trim();
    if (body === "") {
      return;
    }

    setRunning(true);
    appendMessage({
      id: crypto.randomUUID(),
      role: "user",
      title: lane,
      body,
      timestamp: new Date().toISOString(),
    });

    try {
      await bridge.startLane({ lane, prompt: body, workspace });
    } catch (error) {
      setRunning(false);
      appendOperatorMessage("Lane failed", error instanceof Error ? error.message : String(error), "error");
    }
  }

  async function checkMemory() {
    const result = await bridge.runTool({
      tool: "context_status",
      workspace,
      input: { refresh: false, limit: 20 },
    });

    recordCommandResult("context_status", result.status, result.stdout || result.stderr);
  }

  async function runQuickAction(action: QuickAction) {
    await runCommandWithOutput(`idiom ${action.request.command}`, action.request);
  }

  async function runProductCreate() {
    await runCommandWithOutput("idiom product create", {
      command: "product",
      args: ["create", "--effort-root", effortRoot, "--json"],
    });
  }

  async function runProductStatus() {
    await runCommandWithOutput("idiom product status", {
      command: "product",
      args: ["status", "--effort-root", effortRoot, "--operator-view"],
    });
  }

  async function runProductCheck(kind: "lint" | "audit") {
    await runCommandWithOutput(`idiom product ${kind}`, {
      command: "product",
      args: [kind, "--effort-root", effortRoot, "--json"],
    });
  }

  async function runProductTransition(action = transitionAction) {
    await runCommandWithOutput("idiom product transition", {
      command: "product",
      args: ["transition", "--effort-root", effortRoot, "--action", action, "--json"],
    });
  }

  async function runRecoverStatus() {
    await runCommandWithOutput("idiom recover status", {
      command: "recover",
      args: ["status", "--packet", recoveryPacket, "--json"],
    });
  }

  async function runCommandWithOutput(title: string, request: CommandRequest) {
    setRunning(true);
    try {
      const result = await bridge.runCommand({ ...request, workspace });
      recordCommandResult(title, result.status, result.stdout || result.stderr || `exit status ${result.status}`);
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      setCommandOutput(detail);
      appendOperatorMessage(title, detail, "error");
    } finally {
      setRunning(false);
    }
  }

  function recordCommandResult(title: string, status: number, detail: string) {
    setCommandOutput(detail);
    appendOperatorMessage(title, detail, status === 0 ? "success" : "warning");
  }

  function appendOperatorMessage(title: string, body: string, level: ChatMessage["level"] = "info") {
    appendMessage({
      id: crypto.randomUUID(),
      role: "operator",
      title,
      body,
      level,
      timestamp: new Date().toISOString(),
    });
  }

  function appendMessage(message: ChatMessage) {
    setMessages((current) => [...current, message].slice(-160));
  }

  const latestRun = events.find((event) => event.sessionId);
  const failures = events.filter((event) => event.level === "error").length;
  const evidenceCount = events.filter((event) => ["tool", "tool_result", "memory", "contract"].includes(event.type)).length;
  const quickActions = quickActionsByTab[activeTab] ?? [];

  return (
    <main className="app-shell">
      <aside className="sidebar" aria-label="Idiom panels">
        <div className="sidebar-header">
          <div className="brand-mark">id</div>
          <div>
            <strong>Idiom</strong>
            <span>{manifest?.version ?? "loading"}</span>
          </div>
        </div>

        <nav className="panel-nav" aria-label="Panel navigation">
          {sidebarTabs.map((tab) => (
            <button
              key={tab}
              className={activeTab === tab ? "active" : ""}
              onClick={() => setActiveTab(tab)}
            >
              {tab}
            </button>
          ))}
        </nav>

        <div className="sidebar-section">
          <label>
            Lane
            <select value={lane} onChange={(event) => setLane(event.target.value as LaneKind)}>
              {lanes.map((item) => <option key={item} value={item}>{item}</option>)}
            </select>
          </label>
          <label>
            Workspace
            <input value={workspace} onChange={(event) => setWorkspace(event.target.value)} />
          </label>
        </div>

        <SidebarPanel
          activeTab={activeTab}
          quickActions={quickActions}
          running={running}
          effortRoot={effortRoot}
          transitionAction={transitionAction}
          recoveryPacket={recoveryPacket}
          onEffortRootChange={setEffortRoot}
          onTransitionActionChange={setTransitionAction}
          onRecoveryPacketChange={setRecoveryPacket}
          onQuickAction={runQuickAction}
          onCheckMemory={checkMemory}
          onProductCreate={runProductCreate}
          onProductStatus={runProductStatus}
          onProductCheck={runProductCheck}
          onProductTransition={runProductTransition}
          onRecoverStatus={runRecoverStatus}
        />
      </aside>

      <section className="chat-pane" aria-label="Chat">
        <header className="chat-header">
          <div>
            <strong>{lane}</strong>
            <span>{latestRun?.sessionId ? latestRun.sessionId.slice(0, 8) : "no session"}</span>
          </div>
          <div className={`run-state ${running ? "running" : ""}`}>{running ? "running" : "idle"}</div>
        </header>

        <section className="transcript" aria-label="Transcript">
          {messages.map((message) => <ChatBubble key={message.id} message={message} />)}
        </section>

        <section className="composer" aria-label="Composer">
          <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} />
          <div className="composer-actions">
            <button onClick={checkMemory} disabled={running}>Context</button>
            <button className="primary" onClick={startLane} disabled={running || prompt.trim() === ""}>
              {running ? "Running" : "Send"}
            </button>
          </div>
        </section>
      </section>

      <aside className="inspector" aria-label="Run inspector">
        <section className="metric-strip">
          <Metric label="Evidence" value={String(evidenceCount)} tone="blue" />
          <Metric label="Failures" value={String(failures)} tone={failures > 0 ? "red" : "green"} />
          <Metric label="Events" value={String(events.length)} tone="violet" />
        </section>

        <section className="inspector-panel">
          <div className="panel-heading">
            <span>Events</span>
            <small>{events.length}</small>
          </div>
          <div className="event-list">
            {events.map((event) => <EventRow key={event.id} event={event} />)}
          </div>
        </section>

        <section className="inspector-panel output-panel">
          <div className="panel-heading">
            <span>Output</span>
            <small>raw</small>
          </div>
          <pre>{commandOutput}</pre>
        </section>
      </aside>
    </main>
  );
}

function SidebarPanel(props: {
  activeTab: SidebarTab;
  quickActions: QuickAction[];
  running: boolean;
  effortRoot: string;
  transitionAction: string;
  recoveryPacket: string;
  onEffortRootChange: (value: string) => void;
  onTransitionActionChange: (value: string) => void;
  onRecoveryPacketChange: (value: string) => void;
  onQuickAction: (action: QuickAction) => void;
  onCheckMemory: () => void;
  onProductCreate: () => void;
  onProductStatus: () => void;
  onProductCheck: (kind: "lint" | "audit") => void;
  onProductTransition: (action?: string) => void;
  onRecoverStatus: () => void;
}) {
  return (
    <div className="sidebar-section panel-actions">
      {props.activeTab === "memory" ? (
        <button onClick={props.onCheckMemory} disabled={props.running}>Context status</button>
      ) : null}

      {props.quickActions.map((action) => (
        <button key={action.label} onClick={() => props.onQuickAction(action)} disabled={props.running}>
          {action.label}
        </button>
      ))}

      {props.activeTab === "product" ? (
        <div className="operator-controls">
          <label>
            Effort root
            <input value={props.effortRoot} onChange={(event) => props.onEffortRootChange(event.target.value)} placeholder="docs/product-workflows/efforts/started/example" />
          </label>
          <button className="primary-secondary" onClick={props.onProductCreate} disabled={props.running || props.effortRoot.trim() === ""}>Create</button>
          <button onClick={props.onProductStatus} disabled={props.running || props.effortRoot.trim() === ""}>Status</button>
          <button onClick={() => props.onProductCheck("lint")} disabled={props.running || props.effortRoot.trim() === ""}>Lint</button>
          <button onClick={() => props.onProductCheck("audit")} disabled={props.running || props.effortRoot.trim() === ""}>Audit</button>
          <label>
            Transition
            <select value={props.transitionAction} onChange={(event) => props.onTransitionActionChange(event.target.value)}>
              {productTransitionActions.map((action) => <option key={action} value={action}>{action}</option>)}
            </select>
          </label>
          <button className="primary-secondary" onClick={() => props.onProductTransition()} disabled={props.running || props.effortRoot.trim() === ""}>Apply</button>
          <button onClick={() => props.onProductTransition("approve_for_implementation")} disabled={props.running || props.effortRoot.trim() === ""}>Approve</button>
        </div>
      ) : null}

      {props.activeTab === "recover" ? (
        <div className="operator-controls">
          <label>
            Packet
            <input value={props.recoveryPacket} onChange={(event) => props.onRecoveryPacketChange(event.target.value)} placeholder=".idiom/recovery/packet.json" />
          </label>
          <button className="primary-secondary" onClick={props.onRecoverStatus} disabled={props.running || props.recoveryPacket.trim() === ""}>Status</button>
        </div>
      ) : null}
    </div>
  );
}

function ChatBubble({ message }: { message: ChatMessage }) {
  return (
    <article className={`chat-bubble ${message.role} ${message.level ?? "info"}`}>
      <header>
        <strong>{message.title ?? message.role}</strong>
        <time>{formatEventTime(message.timestamp)}</time>
      </header>
      <p>{message.body}</p>
    </article>
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
      </div>
    </article>
  );
}

function messageFromEvent(event: IdiomEvent): ChatMessage {
  return {
    id: event.id,
    role: event.type.startsWith("lane") ? "assistant" : "operator",
    title: event.title,
    body: event.detail ?? event.type,
    timestamp: event.timestamp,
    level: event.level,
  };
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
