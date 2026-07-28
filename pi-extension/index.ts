/**
 * pi extension: tmux-agent-mesh delivery.
 *
 * Pi is the only supported harness that can reach an *idle* agent. Claude Code,
 * Codex and Gemini can only continue a turn that is already ending, so mail for
 * an idle agent there waits for the next human prompt. Pi's extension stays
 * resident for the session, so a filesystem watcher plus sendUserMessage wakes
 * the agent with no keystroke injection anywhere.
 *
 * Deliberately thin. Every policy decision (delivery mode, continuation budget,
 * hop and thread caps, at-most-once claiming) lives in mesh.sh, so one bats
 * suite covers all four harnesses. This file watches, shells out, and speaks.
 *
 * Note on the API surface: registerTool would give the agent a first-class
 * mesh_send tool, but its `parameters` field needs a TypeBox schema and neither
 * typebox nor the pi package resolves from ~/.pi/agent/extensions. So discovery
 * works the same way as the other three harnesses: injected context naming the
 * CLI. Type-only imports are fine because they are erased before jiti runs.
 *
 * Install: symlinked into ~/.pi/agent/extensions/tmux-agent-mesh by
 * agent-mesh.tmux or install.sh. Pi auto-discovers it, so there is no
 * settings.json edit.
 */
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, watch, type FSWatcher } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// Both names must match mesh.sh, which reads MESH_DIR and MESH_NOTIFY_DIR. An
// unprefixed NOTIFY_DIR here moved mesh's flags without moving the watcher, and
// push delivery went silently dead.
const MESH_DIR = process.env.MESH_DIR ?? join(homedir(), ".tmux-agent-mesh");
const NOTIFY_DIR = process.env.MESH_NOTIFY_DIR ?? join(MESH_DIR, "notify");

/** Mirrors _notify_flag in mesh.sh: anything outside [A-Za-z0-9._-] becomes _. */
function notifyFlagName(sessionId: string): string {
  return sessionId.replace(/[^A-Za-z0-9._-]/g, "_") + ".flag";
}

function resolveMeshBin(): string | undefined {
  const candidates = [
    process.env.MESH_BIN,
    join(homedir(), ".local/bin/tmux-agent-mesh"),
    "/usr/local/bin/tmux-agent-mesh",
    "/opt/homebrew/bin/tmux-agent-mesh",
  ].filter(Boolean) as string[];
  for (const c of candidates) {
    if (existsSync(c)) return c;
  }
  try {
    return execFileSync("command", ["-v", "tmux-agent-mesh"], {
      encoding: "utf8",
      shell: "/bin/bash",
    }).trim() || undefined;
  } catch {
    return undefined;
  }
}

const MESH_BIN = resolveMeshBin();

/**
 * Never let a mesh failure take the agent down. A broken mailbox should cost
 * the user their messages, not their session.
 */
function mesh(...args: string[]): string {
  if (!MESH_BIN) return "";
  try {
    return execFileSync(MESH_BIN, args, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
      timeout: 10_000,
    }).trim();
  } catch {
    return "";
  }
}

function meshJson<T>(...args: string[]): T | undefined {
  const out = mesh(...args);
  if (!out) return undefined;
  try {
    const v = JSON.parse(out);
    return v === null ? undefined : (v as T);
  } catch {
    return undefined;
  }
}

export default function (pi: ExtensionAPI) {
  let sessionId: string | undefined;
  let watcher: FSWatcher | undefined;
  let draining = false;

  const deliver = (mode: "push" | "before-start"): string => {
    if (!sessionId) return "";
    return mesh("pi-deliver", "--session", sessionId, "--mode", mode);
  };

  /**
   * The watcher is what makes Pi able to wake from idle. fs.watch fires more
   * than once per touch on some platforms, so `draining` collapses bursts;
   * at-most-once claiming in mesh.sh makes a duplicate harmless anyway.
   */
  const startWatching = (sid: string) => {
    try {
      if (!existsSync(NOTIFY_DIR)) mkdirSync(NOTIFY_DIR, { recursive: true });
      watcher = watch(NOTIFY_DIR, (_event, filename) => {
        if (filename && filename !== notifyFlagName(sid)) return;
        if (draining) return;
        draining = true;
        setTimeout(() => {
          draining = false;
          const text = deliver("push");
          if (text) pi.sendUserMessage(text, { deliverAs: "followUp" });
        }, 50);
      });
    } catch {
      // No watcher means Pi degrades to the pull behaviour of the other
      // harnesses: mail arrives on the next prompt instead of waking the agent.
    }
  };

  pi.on("session_start", async (_event, ctx: ExtensionContext) => {
    try {
      sessionId = ctx.sessionManager.getSessionId();
    } catch {
      return;
    }
    if (!sessionId || !MESH_BIN) return;

    mesh(
      "register",
      "--session", sessionId,
      "--harness", "pi",
      "--pane", process.env.TMUX_PANE ?? "",
      "--cwd", ctx.cwd,
    );

    // A dispatched pane gets its task as the first user message, so a spawned
    // agent starts working without anything being typed into it.
    const claimed = meshJson<{ task: string }>("claim-dispatch", "--session", sessionId);
    if (claimed?.task) {
      pi.sendUserMessage(claimed.task, { deliverAs: "followUp" });
    }

    startWatching(sessionId);
  });

  pi.on("before_agent_start", async () => {
    const text = deliver("before-start");
    if (!text) return {};
    return {
      message: {
        customType: "tmux-agent-mesh",
        content: text,
        display: true,
      },
    };
  });

  /**
   * The continuation budget only means anything if it is reset by *human*
   * input. before_agent_start also fires for the turns mesh itself triggers via
   * sendUserMessage, so resetting there would clear the budget after every push
   * and it could never stop a runaway. `input` is raw typing, which is exactly
   * the signal needed.
   */
  pi.on("input", async () => {
    if (sessionId) mesh("reset-streak", "--session", sessionId);
    return { action: "continue" as const };
  });

  pi.on("session_shutdown", async () => {
    try {
      watcher?.close();
    } catch {
      // closing an already-dead watcher is not worth failing shutdown over
    }
    if (sessionId) mesh("deregister", "--session", sessionId);
  });

  pi.registerCommand("mesh", {
    description: "tmux-agent-mesh: roster, inbox, or send to another agent",
    handler: async (args: string, ctx) => {
      const argv = args.trim();
      if (!argv || argv === "roster") {
        ctx.ui?.info?.(mesh("roster") || "mesh: no roster available");
        return;
      }
      if (argv === "inbox") {
        ctx.ui?.info?.(mesh("inbox") || "mesh: inbox empty");
        return;
      }
      // /mesh <name> <message...>
      const sp = argv.indexOf(" ");
      if (sp < 0) {
        ctx.ui?.info?.("usage: /mesh [roster|inbox] or /mesh <name> <message>");
        return;
      }
      const to = argv.slice(0, sp);
      const body = argv.slice(sp + 1);
      ctx.ui?.info?.(mesh("send", "--to", to, "--message", body) || "mesh: send failed");
    },
  });
}
