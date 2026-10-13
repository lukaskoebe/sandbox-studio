import { ConversationConcept } from "./dm"
import { DeskConcept } from "./desk"
import { TeammateFrame, type Concept } from "./frame"
import { JournalConcept } from "./journal"

/** The teammate page concepts: one frame, three ways to be with a teammate. */
export function TeammateMockup({ concept }: { concept: Concept }) {
  return (
    <TeammateFrame concept={concept}>
      {(presence) =>
        concept === "dm" ? (
          <ConversationConcept presence={presence} />
        ) : concept === "desk" ? (
          <DeskConcept presence={presence} />
        ) : (
          <JournalConcept presence={presence} />
        )
      }
    </TeammateFrame>
  )
}
