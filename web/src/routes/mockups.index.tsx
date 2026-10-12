import { createFileRoute } from "@tanstack/react-router"
import { MockupIndex } from "@/mockups/index-page"

export const Route = createFileRoute("/mockups/")({ component: MockupIndex })
