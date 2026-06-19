import type { IdiomBridge } from "../core/idiom";
import { DemoBridge } from "./demoBridge";
import { isTauriRuntime, TauriBridge } from "./tauriBridge";

export function createBridge(): IdiomBridge {
  if (isTauriRuntime()) {
    return new TauriBridge();
  }

  return new DemoBridge();
}
