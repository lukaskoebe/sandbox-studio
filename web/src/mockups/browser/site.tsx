import {
  createContext,
  useContext,
  type ComponentProps,
  type ReactNode,
} from "react"
import { CheckCircleIcon, ShieldWarningIcon } from "@phosphor-icons/react"
import { cn } from "@/lib/utils"
import type { View } from "./data"

/** The fake page's own size: the remote browser's viewport, scaled to fit like the canvas. */
export const pageWidth = 1024
export const pageHeight = 640

/**
 * A made-up website as the remote browser would show it. It keeps its own colors in dark
 * mode, like any site would. Elements an agent or you act on carry `data-target`.
 */
export function FakeSite({
  view,
  onTarget,
}: {
  view: View
  /** Without it the page is a still picture: no buttons, so it can sit inside one. */
  onTarget?: (target: string) => void
}) {
  const t = (target: string) => ({
    "data-target": target,
    onClick: () => onTarget?.(target),
  })
  return (
    <Still.Provider value={!onTarget}>
      <div
        className="relative overflow-hidden bg-white font-sans text-[14px] text-neutral-800"
        style={{ width: pageWidth, height: pageHeight }}
      >
        {view.page === "login" ? (
          <Login view={view} t={t} />
        ) : view.page === "overview" || view.page === "webhooks" ? (
          <Console view={view} t={t} />
        ) : view.page === "changelog" ? (
          <Changelog />
        ) : view.page === "held" ? (
          <Held url={view.url} />
        ) : (
          <Other url={view.url} />
        )}
      </div>
    </Still.Provider>
  )
}

const Still = createContext(false)

/** A button on the page, or a span in a still picture. */
function Btn(props: ComponentProps<"button">) {
  const still = useContext(Still)
  if (still) return <span {...props} />
  return <button type="button" {...props} />
}

type T = (target: string) => {
  "data-target": string
  onClick: () => void
}

function Logo() {
  return (
    <span className="flex items-center gap-2 text-[15px] font-semibold tracking-tight text-neutral-900">
      <span className="inline-block size-5 rounded-full bg-[#5b5bd6]" />
      Pebble
    </span>
  )
}

function Login({ view, t }: { view: View; t: T }) {
  return (
    <div className="flex h-full items-center justify-center bg-[#f6f6fb]">
      <div className="w-[340px] rounded-xl bg-white p-8 shadow-[0_1px_3px_rgba(0,0,0,.08),0_8px_24px_rgba(0,0,0,.06)]">
        <Logo />
        <h1 className="mt-6 text-[20px] font-semibold text-neutral-900">
          Sign in to your console
        </h1>
        <label className="mt-5 block text-[12px] font-medium text-neutral-600">
          Email
        </label>
        <div
          {...t("email")}
          className="mt-1 h-9 rounded-md border border-neutral-300 px-3 leading-9"
        >
          {view.email ? "finn@agents.invalid" : ""}
        </div>
        <label className="mt-4 block text-[12px] font-medium text-neutral-600">
          Password
        </label>
        <div
          {...t("password")}
          className="mt-1 h-9 rounded-md border border-neutral-300 px-3 leading-9 tracking-[0.2em]"
        >
          {view.password ? "••••••••••••" : ""}
        </div>
        <Btn
          {...t("signin")}
          className="mt-6 h-9 w-full rounded-md bg-[#5b5bd6] font-medium text-white"
        >
          Sign in
        </Btn>
        <p className="mt-4 text-center text-[12px] text-neutral-500">
          Forgot your password?
        </p>
      </div>
    </div>
  )
}

function Console({ view, t }: { view: View; t: T }) {
  const nav = (label: string, target: string, active: boolean) => (
    <Btn
      {...t(target)}
      className={cn(
        "flex h-8 w-full items-center rounded-md px-3 text-left",
        active ? "bg-[#ececfb] font-medium text-[#3e3eb8]" : "text-neutral-600"
      )}
    >
      {label}
    </Btn>
  )
  return (
    <div className="flex h-full">
      <aside className="w-[200px] shrink-0 border-r border-neutral-200 bg-[#fafafc] p-4">
        <Logo />
        <div className="mt-6 grid gap-1">
          {nav("Overview", "nav-overview", view.page === "overview")}
          {nav("Payments", "nav-payments", false)}
          {nav("Webhooks", "nav-webhooks", view.page === "webhooks")}
          {nav("API keys", "nav-keys", false)}
          {nav("Billing", "nav-billing", false)}
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-8">
        {view.page === "overview" ? (
          <Overview />
        ) : (
          <Webhooks view={view} t={t} />
        )}
      </main>
    </div>
  )
}

function Overview() {
  const card = (label: string, value: string) => (
    <div className="rounded-lg border border-neutral-200 p-4">
      <div className="text-[12px] text-neutral-500">{label}</div>
      <div className="mt-1 text-[22px] font-semibold text-neutral-900">
        {value}
      </div>
    </div>
  )
  return (
    <>
      <h1 className="text-[22px] font-semibold text-neutral-900">Overview</h1>
      <p className="mt-1 text-neutral-500">Acme staging · test mode</p>
      <div className="mt-6 grid grid-cols-3 gap-4">
        {card("Payments today", "1,284")}
        {card("Success rate", "99.2%")}
        {card("Webhook deliveries", "3,912")}
      </div>
      <div className="mt-6 h-[220px] rounded-lg border border-neutral-200 bg-[linear-gradient(180deg,#f7f7fe,white)] p-4">
        <div className="text-[12px] text-neutral-500">Last 7 days</div>
        <svg viewBox="0 0 600 160" className="mt-2 h-[160px] w-full">
          <polyline
            fill="none"
            stroke="#5b5bd6"
            strokeWidth="2.5"
            points="0,120 90,100 180,110 270,70 360,80 450,40 540,55 600,30"
          />
        </svg>
      </div>
    </>
  )
}

function Webhooks({ view, t }: { view: View; t: T }) {
  const row = (
    name: string,
    url: string,
    staging: boolean,
    status: ReactNode
  ) => (
    <tr className="border-t border-neutral-200">
      <td className="py-3 font-medium text-neutral-900">{name}</td>
      <td className="py-3 font-mono text-[12px] text-neutral-500">{url}</td>
      <td className="py-3">{status}</td>
      <td className="py-3 text-right">
        <Btn
          {...(staging ? t("test-staging") : {})}
          className="h-8 rounded-md border border-neutral-300 px-3 text-[13px]"
        >
          Send test event
        </Btn>
      </td>
    </tr>
  )
  return (
    <>
      <div className="flex items-center">
        <h1 className="text-[22px] font-semibold text-neutral-900">Webhooks</h1>
        <Btn
          {...t("export")}
          className="ml-auto h-8 rounded-md border border-neutral-300 px-3 text-[13px]"
        >
          Export log
        </Btn>
        <Btn className="ml-2 h-8 rounded-md bg-[#5b5bd6] px-3 text-[13px] text-white">
          Add endpoint
        </Btn>
      </div>
      <p className="mt-1 text-neutral-500">
        Endpoints receive events as signed POST requests.
      </p>
      <table className="mt-6 w-full text-[13px]">
        <thead className="text-left text-[12px] text-neutral-500">
          <tr>
            <th className="pb-2 font-medium">Endpoint</th>
            <th className="pb-2 font-medium">URL</th>
            <th className="pb-2 font-medium">Status</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {row(
            "staging",
            "https://api-staging.acme.test/hooks/pebble",
            true,
            <Pill ok>Enabled</Pill>
          )}
          {row(
            "production",
            "https://api.acme.test/hooks/pebble",
            false,
            <Pill ok>Enabled</Pill>
          )}
          {row(
            "legacy",
            "https://old.acme.test/pebble",
            false,
            <Pill>Disabled</Pill>
          )}
        </tbody>
      </table>
      {view.testSent && (
        <div className="mt-6 flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-[13px] text-emerald-800">
          <CheckCircleIcon weight="fill" className="size-4" />
          Test event <span className="font-mono">payment.succeeded</span> sent
          to staging · 200 OK in 142 ms
        </div>
      )}
    </>
  )
}

function Pill({ ok, children }: { ok?: boolean; children: ReactNode }) {
  return (
    <span
      className={cn(
        "rounded-full px-2 py-0.5 text-[12px]",
        ok
          ? "bg-emerald-50 text-emerald-700"
          : "bg-neutral-100 text-neutral-500"
      )}
    >
      {children}
    </span>
  )
}

function Changelog() {
  return (
    <div className="h-full">
      <header className="flex h-14 items-center border-b border-neutral-200 px-10">
        <Logo />
        <span className="ml-auto flex gap-6 text-neutral-500">
          <span>Docs</span>
          <span className="font-medium text-neutral-900">Changelog</span>
          <span>Pricing</span>
        </span>
      </header>
      <article className="mx-auto max-w-[620px] py-10">
        <p className="text-[12px] text-neutral-500">October 9</p>
        <h1 className="mt-1 text-[26px] font-semibold text-neutral-900">
          Webhook test events now carry real payloads
        </h1>
        <p className="mt-4 leading-relaxed text-neutral-600">
          “Send test event” used to send a <code>ping</code>. It now sends a{" "}
          <code>payment.succeeded</code> event with a sample payload, signed
          with the endpoint's secret, so your handler sees what production
          sends.
        </p>
        <div className="mt-6 h-[140px] rounded-lg bg-neutral-100" />
        <p className="mt-6 leading-relaxed text-neutral-600">
          The event log export now includes the delivery attempt and the
          response time for each event.
        </p>
      </article>
    </div>
  )
}

/** Studio's hold page: the gateway held the request because no rule covers the site. */
function Held({ url }: { url: string }) {
  const h = (() => {
    try {
      return new URL(url).host
    } catch {
      return url
    }
  })()
  return (
    <div className="flex h-full items-center justify-center bg-[#fbfaf7]">
      <div className="max-w-[440px] text-center">
        <span className="mx-auto inline-flex size-12 items-center justify-center rounded-full bg-amber-100 text-amber-700">
          <ShieldWarningIcon className="size-6" />
        </span>
        <h1 className="mt-4 text-[20px] font-semibold text-neutral-900">
          Studio is holding this site
        </h1>
        <p className="mt-2 leading-relaxed text-neutral-600">
          No rule covers <span className="font-mono">{h}</span> yet. Allow it
          above to load the page.
        </p>
      </div>
    </div>
  )
}

function Other({ url }: { url: string }) {
  const h = (() => {
    try {
      return new URL(url).host
    } catch {
      return url
    }
  })()
  return (
    <div className="p-12">
      <h1 className="text-[30px] font-semibold text-neutral-900">{h}</h1>
      <p className="mt-3 max-w-[520px] leading-relaxed text-neutral-600">
        A page from another site, loaded through Studio's gateway.
      </p>
      <div className="mt-8 grid max-w-[620px] gap-3">
        {[80, 95, 60, 88].map((w) => (
          <div
            key={w}
            className="h-3 rounded bg-neutral-100"
            style={{ width: `${w}%` }}
          />
        ))}
      </div>
    </div>
  )
}
