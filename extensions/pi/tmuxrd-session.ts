// installed by tmuxrd
// reinstalling tmuxrd overwrites this file.
//
// Pi keeps its session id in memory and does not leave it on the process.
// This extension writes one JSON file per pi process so the tmuxrd daemon can
// record the id, and so `tmuxrd resume` can start the conversation again
// after the tmux server dies. A kill skips session_shutdown, which is the
// case this file exists for: the record stays until the pid is reused.

import { closeSync, mkdirSync, openSync, readFileSync, renameSync, unlinkSync, writeSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

function reportDir(): string {
  // TMUXRD_STATE_DIR is the ledger root (the directory that contains
  // agents.json). Otherwise the file sits next to that ledger under the
  // default state dir, which is what the daemon reads.
  const override = process.env.TMUXRD_STATE_DIR;
  if (override) return join(override, "pi");
  const base = process.env.XDG_STATE_HOME || join(homedir(), ".local", "state");
  return join(base, "tmuxrd", "pi");
}

function reportPath(): string {
  return join(reportDir(), `${process.pid}.json`);
}

// starttime is field 22 of /proc/self/stat (proc(5)). The daemon compares it
// with the live process so a recycled pid cannot resume the wrong conversation.
function pidStart(): number {
  try {
    const stat = readFileSync("/proc/self/stat", "utf8");
    const end = stat.lastIndexOf(")");
    if (end < 0) return 0;
    const fields = stat.slice(end + 2).trim().split(/\s+/);
    const n = Number(fields[19]);
    return Number.isFinite(n) ? n : 0;
  } catch {
    return 0;
  }
}

function publish(ctx: any): void {
  let id = "";
  let sessionFile = "";
  let cwd = "";
  try {
    const got = ctx?.sessionManager?.getSessionId?.();
    if (typeof got === "string") id = got;
    const file = ctx?.sessionManager?.getSessionFile?.();
    if (typeof file === "string") sessionFile = file;
    const dir = ctx?.sessionManager?.getCwd?.();
    if (typeof dir === "string") cwd = dir;
  } catch {
    return;
  }
  if (!id) return;
  if (!cwd) {
    try {
      cwd = process.cwd();
    } catch {
      cwd = "";
    }
  }
  const body = JSON.stringify({
    session_id: id,
    session_file: sessionFile,
    cwd,
    pid: process.pid,
    pid_start: pidStart(),
  });
  const dir = reportDir();
  mkdirSync(dir, { recursive: true });
  const path = reportPath();
  const tmp = `${path}.tmp`;
  const fd = openSync(tmp, "w", 0o600);
  try {
    writeSync(fd, body);
  } finally {
    closeSync(fd);
  }
  renameSync(tmp, path);
}

function remove(): void {
  try {
    unlinkSync(reportPath());
  } catch {
    // already gone
  }
}

export default function (pi: any) {
  const write = (_event: unknown, ctx: any) => {
    try {
      publish(ctx);
    } catch {
      // a failed report must not break the session
    }
  };
  pi.on("session_start", write);
  // /new and /resume replace the id in this process. session_start fires for
  // those too; agent_start covers a reload that keeps the turn running.
  pi.on("agent_start", write);
  pi.on("session_shutdown", (event: any) => {
    if (event?.reason === "quit") remove();
  });
}
