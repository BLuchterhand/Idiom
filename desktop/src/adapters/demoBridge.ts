import type { CommandRequest, DesktopManifest, EventUnsubscribe, IdiomBridge, IdiomEvent, LaneRequest, ToolInvocation, ToolResult } from "../core/idiom";
import { eventFromText } from "../core/idiom";

type Listener = (event: IdiomEvent) => void;

const sampleEvents: Array<Omit<IdiomEvent, "id" | "timestamp">> = [
  {
    type: "contract",
    level: "success",
    title: "Contract loaded",
    detail: "Workspace allowed, writes guarded, verification required after mutation.",
  },
  {
    type: "memory",
    level: "info",
    title: "Repository memory fresh",
    detail: "991 files indexed, 21k symbols, context packets available.",
  },
  {
    type: "lane",
    level: "info",
    title: "Plan-execute ready",
    detail: "Planner and executor roles are resolved through the active backend profile.",
  },
];

export class DemoBridge implements IdiomBridge {
  private listeners = new Set<Listener>();

  async appManifest(): Promise<DesktopManifest> {
    return {
      appName: "Idiom Desktop",
      idiomBinary: "demo://idiom",
      workspace: "/workspace/idiom",
      version: "desktop-preview",
    };
  }

  async runTool(request: ToolInvocation): Promise<ToolResult> {
    const event = eventFromText({
      type: "tool",
      level: "success",
      title: `${request.tool} completed`,
      detail: "Demo adapter returned a bounded sidecar-style result.",
      payload: request,
    });

    this.emit(event);

    return {
      status: 0,
      stdout: JSON.stringify({ Tool: request.tool, Summary: event.detail }, null, 2),
      stderr: "",
    };
  }

  async runCommand(request: CommandRequest): Promise<ToolResult> {
    const summary = demoCommandSummary(request);
    const event = eventFromText({
      type: "command",
      level: "success",
      title: `idiom ${request.command}`,
      detail: summary,
      payload: request,
    });

    this.emit(event);

    return {
      status: 0,
      stdout: summary,
      stderr: "",
    };
  }

  async startLane(request: LaneRequest) {
    const sessionId = crypto.randomUUID();

    this.emit(eventFromText({
      type: "lane_started",
      level: "info",
      title: `${request.lane} started`,
      detail: request.prompt,
      sessionId,
    }));

    sampleEvents.forEach((event, index) => {
      window.setTimeout(() => this.emit(eventFromText({ ...event, sessionId })), 350 * (index + 1));
    });

    window.setTimeout(() => {
      this.emit(eventFromText({
        type: "lane_completed",
        level: "success",
        title: "Run completed",
        detail: "The harness produced an auditable terminal state.",
        sessionId,
      }));
    }, 1600);

    return { sessionId };
  }

  async subscribe(listener: Listener): Promise<EventUnsubscribe> {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(event: IdiomEvent) {
    this.listeners.forEach((listener) => listener(event));
  }
}

function demoCommandSummary(request: CommandRequest) {
  const args = request.args?.join(" ") ?? "";
  switch (`${request.command} ${args}`.trim()) {
    case "doctor --json":
      return JSON.stringify({ status: "pass", profiles: ["discovery", "plan", "implement", "eval", "sidecar"] }, null, 2);
    case "tool list --profile discovery":
      return "context_pack\ncontext_status\ncontext_query\nharness_audit\nrecovery_status";
    case "tool schemas":
      return JSON.stringify({ schema: "idiom.tool_manifest.v1", tools: ["context_pack", "harness_audit", "submit_plan"] }, null, 2);
    case "audit behavior --json":
      return JSON.stringify({ status: "all_clear", surface: "desktop-demo" }, null, 2);
    case "backend status":
      return "planner: configured\nexecutor: configured\nclassifier: optional";
    case "queue status --json":
      return JSON.stringify({ pools: [], state: "idle" }, null, 2);
    case "product efforts --json":
      return JSON.stringify({ efforts: [], status: "empty" }, null, 2);
    default:
      return `Demo command accepted: idiom ${request.command} ${args}`.trim();
  }
}
