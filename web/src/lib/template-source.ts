export const maxTemplateSourceBytes = 65_536

type TemplateSourceErrorKind = "oversize" | "empty" | "nul" | "utf8"

const templateSourceErrorMessages: Record<TemplateSourceErrorKind, string> = {
  oversize: "The YAML file exceeds 65,536 bytes.",
  empty: "The YAML file is empty.",
  nul: "The YAML file contains a NUL character.",
  utf8: "The YAML file is not valid UTF-8.",
}

export class TemplateSourceError extends Error {
  constructor(readonly kind: TemplateSourceErrorKind) {
    super(templateSourceErrorMessages[kind])
    this.name = "TemplateSourceError"
  }
}

export function templateSourceByteLength(source: string) {
  return new TextEncoder().encode(source).byteLength
}

export function isTemplateSourceDownloadable(source: string) {
  return (
    Boolean(source.trim()) &&
    templateSourceByteLength(source) <= maxTemplateSourceBytes
  )
}

export function decodeTemplateSource(bytes: ArrayBuffer) {
  if (bytes.byteLength > maxTemplateSourceBytes) {
    throw new TemplateSourceError("oversize")
  }

  let source: string
  try {
    source = new TextDecoder("utf-8", {
      fatal: true,
      ignoreBOM: true,
    }).decode(bytes)
  } catch {
    throw new TemplateSourceError("utf8")
  }

  if (!source.trim()) throw new TemplateSourceError("empty")
  if (source.includes("\0")) throw new TemplateSourceError("nul")
  return source
}

export function downloadTemplateSource(source: string) {
  const blob = new Blob([source], { type: "text/yaml;charset=utf-8" })
  const objectUrl = URL.createObjectURL(blob)
  let link: HTMLAnchorElement | undefined

  try {
    link = document.createElement("a")
    link.href = objectUrl
    link.download = "spec.yaml"
    link.hidden = true
    document.body.append(link)
    link.click()
  } finally {
    link?.remove()
    window.setTimeout(() => URL.revokeObjectURL(objectUrl), 0)
  }
}
