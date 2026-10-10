// Managed by Sandbox Studio: rewritten when a session starts.
//
// Forwards OpenCode's session events to `studio-agent hook`, which asks Studio for memory
// context and hands transcripts to extraction (PLAN §6.7). A failing hook never blocks
// OpenCode: every call ends after a few seconds with no context.
//
// verify (S7): the event and hook names and their payloads against opencode-ai 1.18.35:
// session.created (properties.info.id), session.compacted and session.idle
// (properties.sessionID), chat.message, experimental.chat.system.transform and
// experimental.session.compacting, and client.session.messages.

const AGENT = "/opt/studio/bin/studio-agent"
const TIMEOUT_MS = 8000
const MAX_TRANSCRIPT = 768 * 1024 // characters; the agent takes at most 1 MiB of input

async function hook(event, payload) {
  try {
    const proc = Bun.spawn([AGENT, "hook", event, "--harness", "opencode"], {
      stdin: new TextEncoder().encode(JSON.stringify(payload)),
      stdout: "pipe",
      stderr: "ignore",
    })
    const timer = setTimeout(() => proc.kill(), TIMEOUT_MS)
    const out = await new Response(proc.stdout).text()
    clearTimeout(timer)
    await proc.exited
    const reply = JSON.parse(out || "{}")
    return typeof reply.context === "string" ? reply.context : ""
  } catch {
    return ""
  }
}

export const StudioMemory = async ({ client, directory }) => {
  const started = new Map() // sessionID → session-start context
  const prompts = new Map() // sessionID → context for the current prompt

  // The conversation's text so far. Studio keeps an offset per session and only
  // extracts what is new; a long transcript is sent from transcriptOffset on.
  async function transcript(sessionID) {
    try {
      const res = await client.session.messages({ path: { id: sessionID } })
      let text = ""
      for (const m of res.data ?? []) {
        for (const p of m.parts ?? []) {
          if (p.type === "text" && !p.synthetic && p.text) {
            text += `${m.info?.role ?? "unknown"}: ${p.text}\n`
          }
        }
      }
      const offset = Math.max(0, text.length - MAX_TRANSCRIPT)
      return { transcript: text.slice(offset), transcriptOffset: offset }
    } catch {
      return {}
    }
  }

  async function start(sessionID, source) {
    started.set(
      sessionID,
      await hook("session-start", { sessionID, source, cwd: directory }),
    )
  }

  return {
    event: async ({ event }) => {
      const props = event.properties ?? {}
      if (event.type === "session.created" && props.info?.id) {
        await start(props.info.id, "startup")
      } else if (event.type === "session.compacted" && props.sessionID) {
        await start(props.sessionID, "compact")
      } else if (event.type === "session.idle" && props.sessionID) {
        await hook("stop", {
          sessionID: props.sessionID,
          cwd: directory,
          ...(await transcript(props.sessionID)),
        })
      }
    },

    "chat.message": async (input, output) => {
      const sessionID = input.sessionID
      const prompt = (output.parts ?? [])
        .filter((p) => p.type === "text" && !p.synthetic && p.text)
        .map((p) => p.text)
        .join("\n")
      if (!sessionID || !prompt) return
      if (!started.has(sessionID)) await start(sessionID, "resume")
      prompts.set(
        sessionID,
        await hook("user-prompt", { sessionID, prompt, cwd: directory }),
      )
    },

    "experimental.chat.system.transform": async (input, output) => {
      const sessionID = input.sessionID
      if (!sessionID) return
      for (const context of [started.get(sessionID), prompts.get(sessionID)]) {
        if (context) output.system.push(context)
      }
    },

    "experimental.session.compacting": async (input, output) => {
      const sessionID = input.sessionID
      await hook("pre-compact", {
        sessionID,
        cwd: directory,
        ...(await transcript(sessionID)),
      })
      const context = started.get(sessionID)
      if (context) output.context.push(context)
    },
  }
}
