import { Link } from "@tanstack/react-router"
import { pieces, type RefTarget } from "@/lib/lead"

const link =
  "font-medium text-foreground underline decoration-foreground/30 underline-offset-3 hover:decoration-foreground"

/**
 * The model's words with their refs turned into what they stand for: a teammate or a thread
 * links to it, a push names its pull request or branch, a waiting approval its subject. Refs
 * the day doesn't have were dropped when the day was written; any left are left out here too.
 */
export function Prose({
  env,
  text,
  refs,
}: {
  env: string
  text: string
  refs: Map<string, RefTarget>
}) {
  return (
    <>
      {pieces(text).map((p, i) => {
        if ("text" in p) return <span key={i}>{p.text}</span>
        const r = refs.get(p.ref)
        if (!r) return null
        return <RefLink key={i} env={env} target={r} />
      })}
    </>
  )
}

/** One ref, as a link where there is a place to go. */
export function RefLink({
  env,
  target: r,
}: {
  env: string
  target: RefTarget
}) {
  switch (r.kind) {
    case "teammate":
      return r.teammate.id ? (
        <Link
          to="/e/$env/teammates/$id"
          params={{ env, id: r.teammate.id }}
          className={link}
        >
          {r.teammate.name}
        </Link>
      ) : (
        <span className="font-medium">{r.teammate.name}</span>
      )
    case "thread":
      return r.teammate.id ? (
        <Link
          to="/e/$env/teammates/$id"
          params={{ env, id: r.teammate.id }}
          search={{ thread: r.thread.id }}
          className={link}
        >
          {r.thread.title}
        </Link>
      ) : (
        <Link
          to="/e/$env/chats/$chat"
          params={{ env, chat: r.thread.id }}
          className={link}
        >
          {r.thread.title}
        </Link>
      )
    case "push":
      return r.push.prUrl ? (
        <a
          href={r.push.prUrl}
          target="_blank"
          rel="noreferrer"
          className={link}
        >
          #{r.push.prNumber}
        </a>
      ) : (
        <span className="font-mono text-[0.92em]">{r.push.branch}</span>
      )
    case "approval":
      return <span className="font-medium">{r.waiting.subject}</span>
    case "task":
      return <span className="font-medium">{r.title}</span>
  }
}
