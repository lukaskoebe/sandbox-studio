import { access } from "node:fs/promises"
import { studioBinary } from "./studio"

export default async function globalSetup() {
  try {
    await access(studioBinary)
  } catch {
    throw new Error(
      `${studioBinary} is missing. Build it with \`make e2e-bin\` (after \`make web\`), or run the whole suite with \`make e2e\`.`
    )
  }
}
