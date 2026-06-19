import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import type { CommandRequest, CommandResult, DesktopManifest, EventUnsubscribe, IdiomBridge, IdiomEvent, LaneRequest, ToolInvocation, ToolResult } from "../core/idiom";

declare global {
  interface Window {
    __TAURI_INTERNALS__?: unknown;
  }
}

export function isTauriRuntime(): boolean {
  return typeof window !== "undefined" && Boolean(window.__TAURI_INTERNALS__);
}

export class TauriBridge implements IdiomBridge {
  async appManifest(): Promise<DesktopManifest> {
    return invoke<DesktopManifest>("app_manifest");
  }

  async runTool(request: ToolInvocation): Promise<ToolResult> {
    return invoke<ToolResult>("run_tool", { request });
  }

  async runCommand(request: CommandRequest): Promise<CommandResult> {
    return invoke<CommandResult>("run_command", { request });
  }

  async startLane(request: LaneRequest) {
    return invoke<{ sessionId: string }>("start_lane", { request });
  }

  async subscribe(listener: (event: IdiomEvent) => void): Promise<EventUnsubscribe> {
    const unlisten = await listen<IdiomEvent>("idiom://lane-event", (event) => {
      listener(event.payload);
    });

    return unlisten;
  }
}
