import { useState } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Checkbox } from "@/components/ui/checkbox"

const key = "studio.browser.paste-ok"

/** Whether this browser was told to paste into a teammate's browser without asking. */
export function pasteAllowed() {
  try {
    return localStorage.getItem(key) === "1"
  } catch {
    return false
  }
}

function allowPaste() {
  try {
    localStorage.setItem(key, "1")
  } catch {
    // Without storage, the next paste asks again.
  }
}

/**
 * Asks before text from this computer's clipboard goes into a teammate's browser, which may be a
 * password: what the teammate can and can't read of it there.
 */
export function PasteWarning({
  name,
  text,
  onPaste,
  onCancel,
}: {
  name: string
  /** The clipboard's text; the dialog is open while there is one. */
  text?: string
  onPaste: (text: string) => void
  onCancel: () => void
}) {
  const [always, setAlways] = useState(false)
  const chars = [...(text ?? "")].length
  return (
    <AlertDialog
      open={text !== undefined}
      onOpenChange={(open) => !open && onCancel()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Paste into {name}&apos;s browser?</AlertDialogTitle>
          <AlertDialogDescription>
            The {chars} character{chars === 1 ? "" : "s"} on your clipboard go
            to the page as typing, in the field it has focused.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="grid list-disc gap-1.5 pl-5 text-xs/relaxed text-muted-foreground">
          <li>
            In a password field, {name} can&apos;t read it: Studio blanks
            password, one-time code and card fields in what {name} reads of the
            page, and screenshots show them as dots.
          </li>
          <li>
            In any other field, {name} reads it with the page and sees it in
            screenshots.
          </li>
          <li>
            Signed in, the browser stays signed in, and {name} can use the site
            as you, within its site rules.
          </li>
          <li>
            A password kept in Secrets never reaches {name}: {name} asks Studio
            to fill it in.
          </li>
        </ul>
        <label className="flex items-center gap-2 text-xs">
          <Checkbox
            checked={always}
            onCheckedChange={(c) => setAlways(c === true)}
          />
          Don&apos;t ask again in this browser
        </label>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              if (always) allowPaste()
              if (text !== undefined) onPaste(text)
            }}
          >
            Paste
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
