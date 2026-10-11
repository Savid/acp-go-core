package process

import (
	"os"
	"syscall"
)

// ForwardedSignals are the signals a sibling's command binary turns into
// cancellation of its serving context.
func ForwardedSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}
