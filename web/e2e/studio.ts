import { execFile, spawn } from "node:child_process"
import { access, mkdtemp, rm, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import { promisify } from "node:util"

/** The Studio binary built with `-tags e2e` (`make e2e-bin`). */
export const studioBinary = path.resolve(
  import.meta.dirname,
  "../../bin/studio-e2e"
)

/**
 * The listen port of a test Studio. Studio also takes the next two ports (gateway and
 * template registry), so slots are 10 apart, in 18400–18999.
 */
export function studioPort(slot: number) {
  return 18400 + slot * 10
}

export type Studio = {
  url: string
  /** The data directory; its e2e-host-home stands in for the home of the user Studio runs as. */
  home: string
  /** A fresh one-time login link, from `studio login-url`. */
  loginURL: () => Promise<string>
  /** Runs `studio quit` against it. */
  quit: () => Promise<void>
  stop: () => Promise<void>
  /** Unlocks the keychain of a Studio started with keychainLocked. */
  unlockKeychain: () => Promise<void>
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/**
 * Starts a Studio with its own data directory on port. The invalid D-Bus address keeps
 * it away from the OS keychain: its vault key must land in a file in the data directory.
 * With keychainLocked, the e2e build's keychain is locked (cmd/studio/runtime_e2e.go), and
 * Studio waits for it until unlockKeychain.
 */
export async function startStudio(
  port: number,
  { keychainLocked = false }: { keychainLocked?: boolean } = {}
): Promise<Studio> {
  const home = await mkdtemp(path.join(tmpdir(), "studio-e2e-"))
  const lock = path.join(home, "e2e-keychain-locked")
  if (keychainLocked) await writeFile(lock, "")
  const addr = `127.0.0.1:${port}`
  const url = `http://${addr}`
  const env = {
    ...process.env,
    SANDBOX_STUDIO_HOME: home,
    DBUS_SESSION_BUS_ADDRESS:
      "unix:path=/nonexistent/sandbox-studio-scratch-bus",
  }
  const proc = spawn(studioBinary, ["-addr", addr], {
    env,
    stdio: ["ignore", "pipe", "pipe"],
  })
  let log = ""
  proc.stdout.on("data", (d: Buffer) => (log += d.toString()))
  proc.stderr.on("data", (d: Buffer) => (log += d.toString()))
  const exited = new Promise<void>((resolve) =>
    proc.on("exit", () => resolve())
  )

  const stop = async () => {
    if (proc.exitCode === null && proc.signalCode === null) {
      proc.kill("SIGINT")
      const killed = await Promise.race([
        exited.then(() => false),
        sleep(10_000).then(() => true),
      ])
      if (killed) {
        proc.kill("SIGKILL")
        await exited
      }
    }
    await rm(home, { recursive: true, force: true })
  }

  const deadline = Date.now() + 30_000
  for (;;) {
    if (proc.exitCode !== null) {
      await stop()
      throw new Error(`Studio on ${addr} exited:\n${log}`)
    }
    try {
      const res = await fetch(`${url}/api/auth/status`)
      if (res.ok) break
      if (keychainLocked && res.headers.get("X-Studio-Waiting") === "keychain")
        break
    } catch {
      // not listening yet
    }
    if (Date.now() > deadline) {
      await stop()
      throw new Error(`Studio on ${addr} did not start:\n${log}`)
    }
    await sleep(100)
  }
  try {
    if (!keychainLocked) await access(path.join(home, "vault.key"))
  } catch {
    await stop()
    throw new Error(
      `Studio on ${addr} has no vault.key file; it must not use the OS keychain:\n${log}`
    )
  }

  return {
    url,
    home,
    stop,
    unlockKeychain: () => rm(lock, { force: true }),
    loginURL: async () => {
      const { stdout } = await promisify(execFile)(
        studioBinary,
        ["-addr", addr, "login-url"],
        { env }
      )
      return stdout.trim()
    },
    quit: async () => {
      await promisify(execFile)(studioBinary, ["-addr", addr, "quit"], { env })
    },
  }
}
