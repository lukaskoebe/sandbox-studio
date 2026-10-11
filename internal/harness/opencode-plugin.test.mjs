import assert from "node:assert/strict"
import { Buffer } from "node:buffer"
import { EventEmitter } from "node:events"
import { readFile } from "node:fs/promises"
import test from "node:test"
import vm from "node:vm"

const source = await readFile(new URL("./opencode-plugin.js", import.meta.url), "utf8")

function loadPlugin(spawn, timers = { setTimeout, clearTimeout }) {
  const script = source
    .replace('import { spawn } from "node:child_process"', "const spawn = injectedSpawn")
    .replace('import { Buffer } from "node:buffer"', "const Buffer = injectedBuffer")
    .replace("export default {", "globalThis.pluginDefinition = {")
  const context = {
    injectedSpawn: spawn,
    injectedBuffer: Buffer,
    AbortController,
    ...timers,
  }
  vm.runInNewContext(script, context, { filename: "opencode-plugin.js" })
  return context.pluginDefinition
}

function sessionInfo(id, directory) {
  return {
    id,
    directory,
    projectID: "project-test",
    time: { created: 1, updated: 1 },
    title: "Test session",
    version: "1",
  }
}

function v2SessionInfo(id, directory) {
  return {
    id,
    projectID: "project-test",
    location: { directory },
  }
}

function v2UserMessage(id, text) {
  return {
    id,
    time: { created: 1 },
    text,
    type: "user",
  }
}

function v2AssistantMessage(id, text) {
  return {
    id,
    time: { created: 2, completed: 3 },
    type: "assistant",
    agent: "build",
    model: { providerID: "test", id: "model-test" },
    content: [{ type: "text", text }],
  }
}

function sessionMessage(sessionID, id, role, text) {
  return {
    info: {
      id,
      role,
      sessionID,
      time: role === "user" ? { created: 1 } : { created: 1, completed: 2 },
    },
    parts: [{ type: "text", text }],
  }
}

function modelHookEvent(sessionID, system, messages = []) {
  return {
    sessionID,
    agent: "build",
    model: { providerID: "test", id: "model-test" },
    system,
    messages,
    options: {},
    tools: {},
  }
}

function fakeSpawn(replyFor, records = []) {
  return (command, args, options) => {
    const child = new EventEmitter()
    child.stdout = new EventEmitter()
    child.stdin = new EventEmitter()
    child.killSignals = []
    child.kill = (signal) => {
      child.killSignals.push(signal)
      return true
    }
    child.stdin.end = (input) => {
      const record = {
        command,
        args,
        options,
        input: String(input),
        payload: JSON.parse(String(input)),
        child,
      }
      records.push(record)
      queueMicrotask(() => {
        try {
          const reply = replyFor(record.args[1], record.payload)
          child.stdout.emit("data", Buffer.from(JSON.stringify(reply ?? {})))
          child.emit("close", 0)
        } catch (error) {
          child.emit("error", error)
        }
      })
    }
    return child
  }
}

function createEventQueue() {
  const items = []
  const waiters = []
  let signal
  return {
    get signal() {
      return signal
    },
    push(value) {
      const waiter = waiters.shift()
      if (waiter) waiter({ value, done: false })
      else items.push(value)
    },
    subscribe(options) {
      signal = options.signal
      return {
        [Symbol.asyncIterator]() {
          return this
        },
        next() {
          if (items.length) return Promise.resolve({ value: items.shift(), done: false })
          if (signal.aborted) return Promise.resolve({ done: true })
          return new Promise((resolve) => {
            const waiter = (result) => resolve(result)
            waiters.push(waiter)
            signal.addEventListener(
              "abort",
              () => {
                const index = waiters.indexOf(waiter)
                if (index >= 0) waiters.splice(index, 1)
                resolve({ done: true })
              },
              { once: true },
            )
          })
        },
      }
    },
  }
}

async function settleUntil(predicate) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (predicate()) return
    await new Promise((resolve) => setImmediate(resolve))
  }
  assert.fail("plugin callback did not settle")
}

test("exports a dependency-free dual V1 and V2 plugin definition", () => {
  const plugin = loadPlugin(() => {
    throw new Error("unexpected subprocess")
  })
  assert.equal(plugin.id, "sandbox-studio.memory")
  assert.equal(typeof plugin.setup, "function")
  assert.equal(typeof plugin.server, "function")
  assert.doesNotMatch(source, /export\s+(?:const|function)\s+StudioMemory/)
  assert.doesNotMatch(source, /from\s+["']@opencode\/plugin["']/)
})

test("V1 retains prompt/context hooks and bounded transcript extraction", async () => {
  const records = []
  const plugin = loadPlugin(
    fakeSpawn((event) => {
      if (event === "session-start") return { context: "startup memory" }
      if (event === "user-prompt") return { context: "prompt memory" }
      return {}
    }, records),
  )
  const client = {
    session: {
      get: async () => ({ data: { directory: "/workspace/v1-session" } }),
      messages: async () => ({
        data: [
          sessionMessage("ses-v1", "msg-user", "user", "remember this"),
          sessionMessage("ses-v1", "msg-assistant", "assistant", "saved"),
          {
            ...sessionMessage("ses-v1", "msg-internal", "assistant", "internal"),
            parts: [{ type: "text", text: "internal", synthetic: true }],
          },
        ],
      }),
    },
  }
  const hooks = await plugin.server({ client, directory: "/plugin-root" })

  await hooks.event({
    event: { type: "session.created", properties: { info: sessionInfo("ses-v1", "/workspace/v1-session") } },
  })
  await hooks["chat.message"](
    { sessionID: "ses-v1" },
    { parts: [{ type: "text", text: "Where is the setting?" }, { type: "text", text: "synthetic", synthetic: true }] },
  )
  const output = { system: [] }
  await hooks["experimental.chat.system.transform"]({ sessionID: "ses-v1" }, output)
  assert.deepEqual(output.system, ["startup memory", "prompt memory"])

  const compactOutput = { context: [] }
  await hooks["experimental.session.compacting"]({ sessionID: "ses-v1" }, compactOutput)
  assert.deepEqual(compactOutput.context, ["startup memory"])
  await hooks.event({ event: { type: "session.idle", properties: { sessionID: "ses-v1" } } })

  const calls = records.map((record) => [record.args[1], record.payload])
  assert.equal(calls.filter(([event]) => event === "session-start").length, 1)
  assert.equal(records[0].command, "/opt/studio/bin/studio-agent")
  assert.deepEqual(Array.from(records[0].args), ["hook", "session-start", "--harness", "opencode"])
  assert.deepEqual(Array.from(records[0].options.stdio), ["pipe", "pipe", "ignore"])
  assert.deepEqual(calls.find(([event]) => event === "user-prompt")[1], {
    sessionID: "ses-v1",
    prompt: "Where is the setting?",
    cwd: "/workspace/v1-session",
  })
  assert.equal(calls.find(([event]) => event === "pre-compact")[1].transcript, "user: remember this\nassistant: saved\n")
  assert.equal(calls.find(([event]) => event === "stop")[1].cwd, "/workspace/v1-session")
  assert.equal(calls.find(([event]) => event === "stop")[1].transcript, "user: remember this\nassistant: saved\n")
})

test("transcript extraction retains a bounded tail and its original offset", async () => {
  const records = []
  const plugin = loadPlugin(fakeSpawn(() => ({}), records))
  const hooks = await plugin.server({
    directory: "/workspace",
    client: {
      session: {
        messages: async () => ({
          data: [sessionMessage("ses-long", "msg-long", "user", "x".repeat(200 * 1024))],
        }),
      },
    },
  })
  await hooks["experimental.session.compacting"]({ sessionID: "ses-long" }, { context: [] })
  const payload = records[0].payload
  assert.ok(payload.transcript.length <= 128 * 1024)
  assert.ok(payload.transcriptOffset > 0)
  assert.ok(Buffer.byteLength(records[0].input, "utf8") <= 1024 * 1024)
})

test("V2 resolves session directories, injects prompt context, extracts on compact and idle, and aborts events", async () => {
  const records = []
  const plugin = loadPlugin(
    fakeSpawn((event, payload) => {
      if (event === "session-start") return { context: `start:${payload.source}` }
      if (event === "user-prompt") return { context: "prompt memory" }
      return {}
    }, records),
  )
  const registered = new Map()
  const queue = createEventQueue()
  const ctx = {
    location: { directory: "/plugin-root" },
    session: {
      hook: async (name, callback) => {
        registered.set(name, callback)
        return { dispose() {} }
      },
      get: async ({ sessionID }) => v2SessionInfo(sessionID, `/session/${sessionID}`),
      context: async () => [
        v2UserMessage("msg_idle_user", "idle transcript"),
        v2AssistantMessage("msg_idle_assistant", "assistant transcript"),
      ],
    },
    event: { subscribe: (options) => queue.subscribe(options) },
  }
  const cleanup = await plugin.setup(ctx)

  queue.push({
    id: "evt_created",
    type: "session.created",
    created: 1,
    durable: { aggregateID: "ses_v2", seq: 0, version: 1 },
    data: {
      sessionID: "ses_v2",
      projectID: "project-test",
      location: { directory: "/session/from-event" },
      slug: "test-session",
      version: "2.0.26",
    },
  })
  await settleUntil(() => records.some((record) => record.args[1] === "session-start"))
  await registered.get("prompt")({
    sessionID: "ses_v2",
    prompt: { text: "Find the API port" },
  })
  const modelEvent = modelHookEvent("ses_v2", [{ type: "text", text: "prompt memory" }])
  await registered.get("context")(modelEvent)
  await registered.get("context")(modelEvent)
  assert.deepEqual(modelEvent.system.map((part) => ({ type: part.type, text: part.text })), [
    { type: "text", text: "prompt memory" },
    { type: "text", text: "start:startup" },
  ])

  const compactEvent = modelHookEvent("ses_v2", [{ type: "text", text: "start:startup" }], [
    { role: "user", content: [{ type: "text", text: "before compact" }] },
  ])
  await registered.get("compaction")(compactEvent)
  assert.deepEqual(compactEvent.system, [{ type: "text", text: "start:startup" }])

  queue.push({
    id: "evt_compacted",
    type: "session.compacted",
    created: 2,
    data: { sessionID: "ses_v2" },
  })
  await settleUntil(() => records.filter((record) => record.args[1] === "session-start").length === 2)
  queue.push({
    id: "evt_idle",
    type: "session.idle",
    created: 3,
    data: { sessionID: "ses_v2" },
  })
  await settleUntil(() => records.some((record) => record.args[1] === "stop"))
  await cleanup()
  assert.equal(queue.signal.aborted, true)

  const calls = records.map((record) => [record.args[1], record.payload])
  assert.equal(calls.filter(([event]) => event === "session-start").length, 2)
  assert.equal(calls.find(([event]) => event === "session-start")[1].cwd, "/session/from-event")
  const compactStart = calls.find(([event, payload]) => event === "session-start" && payload.source === "compact")
  assert.equal(compactStart[1].cwd, "/session/from-event")
  assert.equal(calls.find(([event]) => event === "user-prompt")[1].cwd, "/session/from-event")
  assert.deepEqual(calls.find(([event]) => event === "pre-compact")[1], {
    sessionID: "ses_v2",
    cwd: "/session/from-event",
    transcript: "user: before compact\n",
    transcriptOffset: 0,
  })
  assert.deepEqual(calls.find(([event]) => event === "stop")[1], {
    sessionID: "ses_v2",
    cwd: "/session/from-event",
    transcript: "user: idle transcript\nassistant: assistant transcript\n",
    transcriptOffset: 0,
  })
})

test("V2 session.moved updates the directory and refreshes session context", async () => {
  const records = []
  const plugin = loadPlugin(fakeSpawn((event, payload) => {
    if (event === "session-start") return { context: `start:${payload.source}` }
    if (event === "user-prompt") return { context: "prompt memory" }
    return {}
  }, records))
  const registered = new Map()
  const queue = createEventQueue()
  const ctx = {
    location: { directory: "/plugin-root" },
    session: {
      hook: async (name, callback) => {
        registered.set(name, callback)
        return { dispose() {} }
      },
      get: async ({ sessionID }) => v2SessionInfo(sessionID, "/session/from-get"),
      context: async () => [],
    },
    event: { subscribe: (options) => queue.subscribe(options) },
  }
  const cleanup = await plugin.setup(ctx)
  queue.push({
    id: "evt_moved_created",
    type: "session.created",
    created: 1,
    durable: { aggregateID: "ses_moved", seq: 0, version: 1 },
    data: {
      sessionID: "ses_moved",
      projectID: "project-test",
      location: { directory: "/session/original" },
      slug: "moved-session",
      version: "2.0.26",
    },
  })
  await settleUntil(() => records.some((record) => record.args[1] === "session-start"))
  await registered.get("prompt")({ sessionID: "ses_moved", prompt: { text: "before move" } })

  queue.push({
    id: "evt_moved",
    type: "session.moved",
    created: 2,
    durable: { aggregateID: "ses_moved", seq: 1, version: 1 },
    data: {
      sessionID: "ses_moved",
      location: { directory: "/session/moved" },
      projectID: "project-test",
    },
  })
  await settleUntil(() => records.some((record) =>
    record.args[1] === "session-start" &&
    record.payload.source === "resume" &&
    record.payload.cwd === "/session/moved",
  ))

  const context = modelHookEvent("ses_moved", [])
  await registered.get("context")(context)
  assert.deepEqual(context.system.map((part) => part.text), ["start:resume"])
  const prompt = records.find((record) => record.args[1] === "user-prompt")
  assert.equal(prompt.payload.cwd, "/session/original")
  const starts = records.filter((record) => record.args[1] === "session-start")
  assert.deepEqual(starts.map((record) => [record.payload.source, record.payload.cwd]), [
    ["startup", "/session/original"],
    ["resume", "/session/moved"],
  ])
  await cleanup()
})

test("V2 session.deleted clears cached state from data.sessionID", async () => {
  const records = []
  const plugin = loadPlugin(fakeSpawn((event, payload) => {
    if (event === "session-start") return { context: `start:${payload.source}` }
    return {}
  }, records))
  const registered = new Map()
  const queue = createEventQueue()
  const ctx = {
    location: { directory: "/plugin-root" },
    session: {
      hook: async (name, callback) => {
        registered.set(name, callback)
        return { dispose() {} }
      },
      get: async ({ sessionID }) => v2SessionInfo(sessionID, "/session/deleted"),
      context: async () => [],
    },
    event: { subscribe: (options) => queue.subscribe(options) },
  }
  const cleanup = await plugin.setup(ctx)
  queue.push({
    id: "evt_delete_created",
    type: "session.created",
    created: 1,
    durable: { aggregateID: "ses_deleted", seq: 0, version: 1 },
    data: {
      sessionID: "ses_deleted",
      projectID: "project-test",
      location: { directory: "/session/deleted" },
      slug: "deleted-session",
      version: "2.0.26",
    },
  })
  await settleUntil(() => records.some((record) => record.args[1] === "session-start"))
  queue.push({
    id: "evt_deleted",
    type: "session.deleted",
    created: 2,
    durable: { aggregateID: "ses_deleted", seq: 1, version: 2 },
    data: { sessionID: "ses_deleted" },
  })
  queue.push({
    id: "evt_recreated",
    type: "session.created",
    created: 3,
    durable: { aggregateID: "ses_deleted", seq: 2, version: 1 },
    data: {
      sessionID: "ses_deleted",
      projectID: "project-test",
      location: { directory: "/session/recreated" },
      slug: "recreated-session",
      version: "2.0.26",
    },
  })
  await settleUntil(() => records.filter((record) => record.args[1] === "session-start").length === 2)

  const starts = records.filter((record) => record.args[1] === "session-start")
  assert.deepEqual(starts.map((record) => [record.payload.source, record.payload.cwd]), [
    ["startup", "/session/deleted"],
    ["startup", "/session/recreated"],
  ])
  await cleanup()
})

test("V2 context resumes a reloaded session without a prompt and deduplicates concurrent starts", async () => {
  const records = []
  const plugin = loadPlugin(fakeSpawn((event, payload) => {
    if (event === "session-start") return { context: `start:${payload.source}` }
    return {}
  }, records))
  const registered = new Map()
  const queue = createEventQueue()
  const ctx = {
    location: { directory: "/plugin-root" },
    session: {
      hook: async (name, callback) => {
        registered.set(name, callback)
        return { dispose() {} }
      },
      get: async ({ sessionID }) => v2SessionInfo(sessionID, `/session/${sessionID}`),
      context: async () => [],
    },
    event: { subscribe: (options) => queue.subscribe(options) },
  }
  const cleanup = await plugin.setup(ctx)
  const first = modelHookEvent("ses_reloaded", [])
  const continuation = modelHookEvent("ses_reloaded", [])
  await Promise.all([
    registered.get("context")(first),
    registered.get("context")(continuation),
  ])

  assert.deepEqual(first.system.map((part) => part.text), ["start:resume"])
  assert.deepEqual(continuation.system.map((part) => part.text), ["start:resume"])
  const starts = records.filter((record) => record.args[1] === "session-start")
  assert.equal(starts.length, 1)
  assert.equal(starts[0].payload.sessionID, "ses_reloaded")
  assert.equal(starts[0].payload.source, "resume")
  assert.equal(starts[0].payload.cwd, "/session/ses_reloaded")
  await cleanup()
})

test("V2 fails setup when a required hook or event subscription cannot register", async () => {
  const plugin = loadPlugin(() => {
    throw new Error("unexpected subprocess")
  })
  const session = {
    hook: async (name) => {
      if (name === "context") throw new Error("context hook registration failed")
      return { dispose() {} }
    },
  }
  await assert.rejects(
    plugin.setup({
      session,
      event: { subscribe: () => createEventQueue().subscribe({ signal: new AbortController().signal }) },
    }),
    /context hook registration failed/,
  )

  await assert.rejects(
    plugin.setup({
      session: { hook: async () => ({ dispose() {} }) },
      event: { subscribe: () => { throw new Error("event subscription failed") } },
    }),
    /event subscription failed/,
  )
})

test("V2 resumes sessions through the prompt hook and fails open when the hook process is unavailable", async () => {
  const records = []
  const plugin = loadPlugin(fakeSpawn(() => {
    throw new Error("studio-agent unavailable")
  }, records))
  const registered = new Map()
  const ctx = {
    location: { directory: "/plugin-root" },
    session: {
      hook: async (name, callback) => {
        registered.set(name, callback)
        return { dispose() {} }
      },
      get: async ({ sessionID }) => v2SessionInfo(sessionID, "/resumed-session"),
      context: async () => [],
    },
    event: { subscribe: () => createEventQueue().subscribe({ signal: new AbortController().signal }) },
  }
  const cleanup = await plugin.setup(ctx)
  await registered.get("prompt")({ sessionID: "ses_resumed", prompt: { text: "Continue" } })
  const modelEvent = modelHookEvent("ses_resumed", [])
  await registered.get("context")(modelEvent)
  assert.deepEqual(modelEvent.system, [])
  await cleanup()

  const starts = records.filter((record) => record.args[1] === "session-start")
  const prompts = records.filter((record) => record.args[1] === "user-prompt")
  assert.equal(starts.length, 1)
  assert.equal(starts[0].payload.source, "resume")
  assert.equal(starts[0].payload.cwd, "/resumed-session")
  assert.equal(prompts[0].payload.cwd, "/resumed-session")
})

// A hung or noisy child must not stall OpenCode or inject an unbounded response.
test("hook subprocess failures release the timer and return no context", async (t) => {
  for (const mode of ["timeout", "oversized", "malformed", "nonzero"]) {
    await t.test(mode, async () => {
      let timerCleared = false
      const child = new EventEmitter()
      child.stdout = new EventEmitter()
      child.stdin = new EventEmitter()
      const kills = []
      child.kill = (signal) => kills.push(signal)
      child.stdin.end = () => queueMicrotask(() => {
        if (mode === "timeout") return
        const body = mode === "oversized" ? "x".repeat(128 * 1024 + 1)
          : mode === "malformed" ? "invalid JSON" : '{"context":"discard on failure"}'
        child.stdout.emit("data", Buffer.from(body))
        if (mode !== "oversized") child.emit("close", mode === "nonzero" ? 1 : 0)
      })
      const plugin = loadPlugin(() => child, {
        setTimeout(callback, delay) {
          assert.equal(delay, 8000)
          if (mode === "timeout") queueMicrotask(callback)
          return 1
        },
        clearTimeout() { timerCleared = true },
      })
      const hooks = await plugin.server({ client: {}, directory: "/workspace" })
      await hooks.event({ event: { type: "session.created", properties: { info: sessionInfo("ses-fail", "/workspace") } } })
      const output = { system: [] }
      await hooks["experimental.chat.system.transform"]({ sessionID: "ses-fail" }, output)
      assert.deepEqual(output.system, [])
      assert.equal(timerCleared, true)
      if (mode === "timeout" || mode === "oversized") assert.ok(kills.includes("SIGKILL"))
    })
  }
})
