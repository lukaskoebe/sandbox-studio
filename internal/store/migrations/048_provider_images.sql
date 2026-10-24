-- Whether the model of an OpenAI-compatible provider takes images, such as the screenshot of
-- browser notes (docs/teammates.md). The models of the other kinds all do. An endpoint's model
-- is taken as text only until the user says otherwise: many local models are.
ALTER TABLE providers ADD COLUMN images INTEGER NOT NULL DEFAULT 0;
