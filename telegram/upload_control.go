package telegram

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type uploadControl struct {
	disabled   atomic.Bool
	mu         sync.Mutex
	timer      *time.Timer
	generation uint64
	logger     *slog.Logger
}

func newUploadControl(logger *slog.Logger) *uploadControl {
	return &uploadControl{logger: logger}
}

func (control *uploadControl) enabled() bool {
	return !control.disabled.Load()
}

func (control *uploadControl) setEnabled(enabled bool, duration time.Duration) {
	control.mu.Lock()
	defer control.mu.Unlock()

	control.generation++

	generation := control.generation

	if control.timer != nil {
		control.timer.Stop()
		control.timer = nil
	}

	control.disabled.Store(!enabled)

	if !enabled || duration <= 0 {
		return
	}

	control.timer = time.AfterFunc(duration, func() {
		control.mu.Lock()
		defer control.mu.Unlock()

		if generation != control.generation {
			return
		}

		control.timer = nil
		control.disabled.Store(true)
		control.logger.Info("Disabled on timer")
	})
}
