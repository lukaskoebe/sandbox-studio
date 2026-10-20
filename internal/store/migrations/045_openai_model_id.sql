-- The default model of OpenAI teammates was saved as gpt-sol-6.1, which OpenAI doesn't know: the
-- model is gpt-6.1-sol. Teammates that have the wrong ID get the right one.
UPDATE teammates SET model = 'gpt-6.1-sol' WHERE model = 'gpt-sol-6.1';
