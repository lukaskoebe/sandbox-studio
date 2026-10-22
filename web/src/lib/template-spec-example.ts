import webAppExample from "./example-template.yaml?raw"
import studioDevExample from "./studio-dev-template.yaml?raw"

/** The "Web app" example spec. New builds start from it, and the editor can insert it. */
export const exampleTemplateSource = webAppExample

/** The examples the editor can insert, by name (docs/template-builds.md). */
export const templateExamples = [
  { name: "Web app", source: webAppExample },
  { name: "Studio dev", source: studioDevExample },
]
