package updates

import "errors"

// inPlace is whether Studio restarts into an update by replacing its own process. Windows
// can't, so the old process starts the new binary and watches it (RestartUnsupervised).
const inPlace = false

func execInPlace(string, []string) error { return errors.ErrUnsupported }
