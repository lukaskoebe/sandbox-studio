import { useLayoutEffect, useRef, useState } from "react"
import { autocompletion } from "@codemirror/autocomplete"
import { history } from "@codemirror/commands"
import { yaml } from "@codemirror/lang-yaml"
import {
  bracketMatching,
  indentOnInput,
  indentUnit,
  syntaxHighlighting,
} from "@codemirror/language"
import { linter, lintGutter, type Diagnostic } from "@codemirror/lint"
import { Compartment, EditorState, Prec, Transaction } from "@codemirror/state"
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view"
import { FileTextIcon } from "@phosphor-icons/react"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { $api, errorMessage, fetchClient } from "@/lib/api/client"
import {
  codeEditorKeyBindings,
  codeEditorTheme,
  codeHighlightStyle,
  editorChangeListener,
} from "@/lib/code-editor"
import { templateExamples } from "@/lib/template-spec-example"
import {
  templateSpecCompletion,
  templateSpecDiagnostics,
  type SpecVocabulary,
} from "@/lib/template-spec-editor"
import {
  maxTemplateSourceBytes,
  templateSourceByteLength,
} from "@/lib/template-source"
import { cn } from "@/lib/utils"

/**
 * The template spec editor. It owns its document: `value` seeds the editor, and
 * the parent remounts it with a new key to replace the source.
 */
export function TemplateSpecEditor({
  env,
  value,
  invalid,
  disabled,
  describedBy,
  onChange,
}: {
  env: string
  value: string
  invalid: boolean
  disabled: boolean
  describedBy: string
  onChange: (value: string) => void
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<EditorView | null>(null)
  const initialValueRef = useRef(value)
  const disabledRef = useRef(disabled)
  const onChangeRef = useRef(onChange)
  const envRef = useRef(env)
  const vocabularyRef = useRef<SpecVocabulary | undefined>(undefined)
  const [readOnlyCompartment] = useState(() => new Compartment())
  const vocabulary = $api.useQuery(
    "get",
    "/api/template-spec/vocabulary",
    {},
    { staleTime: Infinity }
  )

  onChangeRef.current = onChange
  envRef.current = env
  vocabularyRef.current = vocabulary.data && {
    topLevelKeys: vocabulary.data.topLevelKeys ?? [],
    resourceKeys: vocabulary.data.resourceKeys ?? [],
    tools: vocabulary.data.tools ?? [],
    sizeUnits: vocabulary.data.sizeUnits ?? [],
  }

  useLayoutEffect(() => {
    const host = hostRef.current
    if (!host) return

    const doc = initialValueRef.current
    const view = new EditorView({
      state: EditorState.create({
        doc,
        extensions: [
          // Keep CRLF line breaks in the draft when the user edits a CRLF file.
          doc.includes("\r\n") ? EditorState.lineSeparator.of("\r\n") : [],
          yaml(),
          syntaxHighlighting(codeHighlightStyle),
          codeEditorTheme,
          lineNumbers(),
          EditorView.lineWrapping,
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          bracketMatching(),
          indentUnit.of("  "),
          indentOnInput(),
          readOnlyCompartment.of(EditorState.readOnly.of(disabledRef.current)),
          autocompletion({
            defaultKeymap: false,
            override: [templateSpecCompletion(() => vocabularyRef.current)],
          }),
          linter((target) => validateSource(envRef.current, target), {
            delay: 400,
          }),
          lintGutter(),
          EditorView.contentAttributes.of({
            "aria-label": "Template spec",
            "aria-describedby": describedBy,
            spellcheck: "false",
          }),
          editorChangeListener(onChangeRef),
          Prec.highest(keymap.of(codeEditorKeyBindings)),
        ],
      }),
      parent: host,
    })

    editorRef.current = view
    view.focus()
    return () => {
      view.destroy()
      if (editorRef.current === view) editorRef.current = null
    }
  }, [describedBy, readOnlyCompartment])

  useLayoutEffect(() => {
    editorRef.current?.dispatch({
      effects: readOnlyCompartment.reconfigure(
        EditorState.readOnly.of(disabled)
      ),
    })
  }, [disabled, readOnlyCompartment])

  function insertExample(source: string) {
    const view = editorRef.current
    if (!view) return

    view.dispatch({
      changes: {
        from: 0,
        to: view.state.doc.length,
        insert: source,
      },
      scrollIntoView: true,
      annotations: Transaction.userEvent.of("input"),
    })
    view.focus()
  }

  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex justify-end">
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={disabled}
                className="text-foreground"
              />
            }
          >
            <FileTextIcon />
            Insert example
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-40">
            {templateExamples.map((example) => (
              <DropdownMenuItem
                key={example.name}
                onClick={() => insertExample(example.source)}
              >
                {example.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div
        ref={hostRef}
        className={cn(
          "w-full min-w-0 rounded-md border bg-background",
          invalid
            ? "border-destructive ring-2 ring-destructive/20"
            : "border-input"
        )}
      />
      {!value.trim() && (
        <div className="flex flex-wrap items-center gap-2 text-xs/relaxed text-muted-foreground">
          <span>The spec is empty.</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={disabled}
            onClick={() => insertExample(templateExamples[0].source)}
          >
            Insert the Web app example
          </Button>
        </div>
      )}
    </div>
  )
}

/** Asks the validate endpoint for the problems in the document. */
async function validateSource(
  env: string,
  view: EditorView
): Promise<Diagnostic[]> {
  const { doc } = view.state
  const source = doc.toString()
  if (
    !source.trim() ||
    templateSourceByteLength(source) > maxTemplateSourceBytes
  ) {
    return []
  }

  const result = await fetchClient.POST(
    "/api/environments/{env}/builds/validate",
    { params: { path: { env } }, body: { source } }
  )
  if (result.error) throw new Error(errorMessage(result.error))
  return templateSpecDiagnostics(doc, result.data.problems ?? [])
}
