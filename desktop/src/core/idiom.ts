export type LaneKind = "plan-execute" | "implement" | "atom-spec";

export type EventLevel = "info" | "success" | "warning" | "error";

export interface DesktopManifest {
  appName: string;
  idiomBinary: string;
  workspace: string;
  version: string;
}

export interface ToolInvocation {
  tool: string;
  input: unknown;
  workspace?: string;
}

export interface ToolResult {
  status: number;
  stdout: string;
  stderr: string;
}

export interface CommandRequest {
  command: string;
  args?: string[];
  workspace?: string;
}

export type CommandResult = ToolResult;

export interface LaneRequest {
  lane: LaneKind;
  prompt: string;
  workspace?: string;
  backendProfile?: string;
}

export interface LaneHandle {
  sessionId: string;
}

export interface IdiomEvent {
  id: string;
  sessionId?: string;
  type: string;
  level: EventLevel;
  title: string;
  detail?: string;
  timestamp: string;
  payload?: unknown;
}

export type EventUnsubscribe = () => void;

export interface IdiomBridge {
  appManifest(): Promise<DesktopManifest>;
  runCommand(request: CommandRequest): Promise<CommandResult>;
  runTool(request: ToolInvocation): Promise<ToolResult>;
  startLane(request: LaneRequest): Promise<LaneHandle>;
  subscribe(listener: (event: IdiomEvent) => void): Promise<EventUnsubscribe>;
}

export function eventFromText(partial: Partial<IdiomEvent> & Pick<IdiomEvent, "title">): IdiomEvent {
  return {
    id: partial.id ?? crypto.randomUUID(),
    type: partial.type ?? "operator",
    level: partial.level ?? "info",
    title: partial.title,
    detail: partial.detail,
    timestamp: partial.timestamp ?? new Date().toISOString(),
    payload: partial.payload,
    sessionId: partial.sessionId,
  };
}
