package cli

import "context"

// contextFromCommand returns the context used by command handlers.
// Today this is just context.Background; we route through one helper so
// future signal-aware contexts (cancel on SIGINT) plug in cleanly.
func contextFromCommand(_ IOStreams) context.Context {
	return context.Background()
}
