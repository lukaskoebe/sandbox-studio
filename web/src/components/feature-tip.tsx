import type { ReactNode, RefObject } from "react"
import { Popover as PopoverPrimitive } from "@base-ui/react/popover"
import { Button } from "@/components/ui/button"
import {
  onboardingFeatures,
  useFeatureTip,
  type FeatureKey,
} from "@/lib/onboarding"

/**
 * The first-time tip of a feature, beside the control it explains (docs/frontend.md#onboarding).
 * It shows once: Got it closes it for good, and Stop tips closes them all. While another tip
 * shows, this one waits.
 */
export function FeatureTip({
  env,
  feature,
  anchor,
  active = true,
  when,
  title,
  children,
  action,
  side = "left",
  align = "start",
}: {
  env: string | undefined
  feature: FeatureKey
  /** The control the tip points at. */
  anchor: RefObject<Element | null>
  /** Whether the feature is on screen to explain. */
  active?: boolean
  /** What brought the tip, as "first request". */
  when: string
  title: string
  children: ReactNode
  /** A button in place of Got it, which also closes the tip. */
  action?: { label: string; onClick: () => void }
  side?: "top" | "bottom" | "left" | "right"
  align?: "start" | "center" | "end"
}) {
  const tip = useFeatureTip(env, feature, active)
  const n = onboardingFeatures.findIndex((f) => f.key === feature) + 1
  const name = onboardingFeatures[n - 1]?.title
  return (
    <PopoverPrimitive.Root open={tip.show} modal={false}>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Positioner
          anchor={anchor}
          side={side}
          align={align}
          sideOffset={10}
          className="isolate z-50 outline-none"
        >
          <PopoverPrimitive.Popup
            data-testid="feature-tip"
            data-feature={feature}
            initialFocus={false}
            className="grid w-72 max-w-[calc(100vw-1rem)] gap-1.5 rounded-lg bg-popover p-3 text-xs/relaxed text-popover-foreground shadow-lg ring-1 ring-primary outline-none"
          >
            <PopoverPrimitive.Arrow className="size-2.5 rotate-45 border-primary bg-popover data-[side=bottom]:-top-1.5 data-[side=bottom]:border-t data-[side=bottom]:border-l data-[side=left]:-right-1.5 data-[side=left]:border-t data-[side=left]:border-r data-[side=right]:-left-1.5 data-[side=right]:border-b data-[side=right]:border-l data-[side=top]:-bottom-1.5 data-[side=top]:border-r data-[side=top]:border-b" />
            <p className="flex items-center gap-2 section-label text-primary">
              {n} of {onboardingFeatures.length} · {name}
              <span className="ml-auto tracking-normal text-muted-foreground normal-case">
                {when}
              </span>
            </p>
            <PopoverPrimitive.Title className="text-sm font-medium">
              {title}
            </PopoverPrimitive.Title>
            <PopoverPrimitive.Description
              render={<div />}
              className="text-muted-foreground"
            >
              {children}
            </PopoverPrimitive.Description>
            <div className="mt-1 flex items-center justify-end gap-1.5">
              <Button size="xs" variant="ghost" onClick={tip.stopTips}>
                Stop tips
              </Button>
              {action && (
                <Button size="xs" variant="outline" onClick={tip.close}>
                  Later
                </Button>
              )}
              <Button
                size="xs"
                onClick={() => {
                  tip.close()
                  action?.onClick()
                }}
              >
                {action?.label ?? "Got it"}
              </Button>
            </div>
          </PopoverPrimitive.Popup>
        </PopoverPrimitive.Positioner>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}

/** The tip of a held request's or a push's card: the first one the user meets. */
export function ApprovalTip({
  env,
  feature,
  anchor,
}: {
  env: string
  feature: "network" | "push"
  anchor: RefObject<Element | null>
}) {
  return feature === "network" ? (
    <FeatureTip
      env={env}
      feature="network"
      anchor={anchor}
      when="first request"
      title="A request waits for you"
    >
      Nothing leaves a sandbox unless a rule or you allow it, and the request
      waits while you decide. Allow lets it through; Options make your answer a
      rule, and Network lists the rules.
    </FeatureTip>
  ) : (
    <FeatureTip
      env={env}
      feature="push"
      anchor={anchor}
      when="first push"
      title="Every push waits for your review"
    >
      Review the commits, comment on lines, then push. A pull request your
      teammate opens waits here too, and it reads the reviews and comments in{" "}
      <code>~/forge</code>.
    </FeatureTip>
  )
}
