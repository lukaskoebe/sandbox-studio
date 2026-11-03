import * as React from "react"
import { cn } from "@/lib/utils"
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area"

function Table({ className, ...props }: React.ComponentProps<"table">) {
  return (
    <ScrollArea data-slot="table-container" className="w-full">
      <table
        data-slot="table"
        className={cn("w-full caption-bottom text-xs", className)}
        {...props}
      />
      <ScrollBar orientation="horizontal" />
    </ScrollArea>
  )
}

function TableHeader({ className, ...props }: React.ComponentProps<"thead">) {
  return (
    <thead
      data-slot="table-header"
      className={cn("[&_tr]:border-b [&_tr]:hover:bg-transparent", className)}
      {...props}
    />
  )
}

function TableBody({ className, ...props }: React.ComponentProps<"tbody">) {
  return (
    <tbody
      data-slot="table-body"
      className={cn("[&_tr:last-child]:border-0", className)}
      {...props}
    />
  )
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
  return (
    <tfoot
      data-slot="table-footer"
      className={cn(
        "border-t bg-muted/50 font-medium [&>tr]:last:border-b-0",
        className
      )}
      {...props}
    />
  )
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
  return (
    <tr
      data-slot="table-row"
      className={cn(
        "group/row h-(--wb-row) border-b transition-colors hover:bg-muted/50 has-aria-expanded:bg-muted/50 data-[state=selected]:bg-muted",
        className
      )}
      {...props}
    />
  )
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
  return (
    <th
      data-slot="table-head"
      className={cn(
        "h-(--wb-header) px-2 text-left align-middle font-medium whitespace-nowrap text-foreground [&:has([role=checkbox])]:pr-0",
        className
      )}
      {...props}
    />
  )
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
  return (
    <td
      data-slot="table-cell"
      className={cn(
        "p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0",
        className
      )}
      {...props}
    />
  )
}

function TableCaption({
  className,
  ...props
}: React.ComponentProps<"caption">) {
  return (
    <caption
      data-slot="table-caption"
      className={cn("mt-4 text-xs text-muted-foreground", className)}
      {...props}
    />
  )
}

/**
 * A column that stays at the right edge while the table scrolls sideways. Its cells are opaque,
 * so they take the row's hover tint as an opaque color of their own. onCard is for a table on a
 * card rather than on the page.
 */
const stickyEnd =
  "sticky right-0 bg-background transition-colors group-hover/row:bg-[color-mix(in_oklab,var(--muted)_50%,var(--background))] [thead_&]:group-hover/row:bg-background"
const stickyEndOnCard =
  "sticky right-0 bg-card transition-colors group-hover/row:bg-[color-mix(in_oklab,var(--muted)_50%,var(--card))] [thead_&]:group-hover/row:bg-card"

export {
  stickyEnd,
  stickyEndOnCard,
  Table,
  TableHeader,
  TableBody,
  TableFooter,
  TableHead,
  TableRow,
  TableCell,
  TableCaption,
}
