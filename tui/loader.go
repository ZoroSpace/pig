// tui/loader.go (pi: packages/tui/src/components/loader.ts)
//
// Loader component that updates with an optional spinning animation.
//
// Concurrency note (pig adaptation, declared): pi uses setInterval on the JS
// event loop; pig runs the animation on a timer goroutine. Text's render
// cache is mutex-guarded (see text.go); the loader's own animation state is
// guarded by its own mutex.
package tui

import (
	"sync"
	"time"
)

// LoaderIndicatorOptions mirrors pi's LoaderIndicatorOptions.
type LoaderIndicatorOptions struct {
	/** Animation frames. Use an empty array to hide the indicator. */
	Frames []string
	/** Frame interval in milliseconds for animated indicators. */
	IntervalMs int
}

const (
	defaultLoaderFrames     = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	defaultLoaderIntervalMs = 80
)

// defaultLoaderFrameList materializes pi's DEFAULT_FRAMES slice.
func defaultLoaderFrameList() []string {
	frames := make([]string, 0, len([]rune(defaultLoaderFrames)))
	for _, r := range defaultLoaderFrames {
		frames = append(frames, string(r))
	}
	return frames
}

// Loader is the spinning loader (pi: class Loader extends Text).
type Loader struct {
	Text

	mu                      sync.Mutex
	frames                  []string
	intervalMs              int
	currentFrame            int
	stopCh                  chan struct{}
	ui                      TUI
	renderIndicatorVerbatim bool
	spinnerColorFn          func(str string) string
	messageColorFn          func(str string) string
	message                 string
}

// NewLoader mirrors pi's constructor (indicator may be nil).
func NewLoader(ui TUI, spinnerColorFn func(string) string, messageColorFn func(string) string, message string, indicator *LoaderIndicatorOptions) *Loader {
	l := &Loader{
		// pi: super("", 1, 0) — same package, so fields initialize directly
		// (avoids copying the embedded Text, which contains a mutex).
		Text:           Text{paddingX: 1, paddingY: 0},
		ui:             ui,
		spinnerColorFn: spinnerColorFn,
		messageColorFn: messageColorFn,
		message:        message,
	}
	l.SetIndicator(indicator)
	return l
}

func (l *Loader) Render(width int) []string {
	return append([]string{""}, l.Text.Render(width)...)
}

func (l *Loader) Start() {
	l.updateDisplay()
	l.restartAnimation()
}

func (l *Loader) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopLocked()
}

// stopLocked stops the animation goroutine. Caller holds l.mu.
func (l *Loader) stopLocked() {
	if l.stopCh != nil {
		close(l.stopCh)
		l.stopCh = nil
	}
}

func (l *Loader) SetMessage(message string) {
	l.mu.Lock()
	l.message = message
	l.mu.Unlock()
	l.updateDisplay()
}

func (l *Loader) SetIndicator(indicator *LoaderIndicatorOptions) {
	l.mu.Lock()
	l.renderIndicatorVerbatim = indicator != nil
	if indicator != nil && indicator.Frames != nil {
		l.frames = append([]string{}, indicator.Frames...)
	} else {
		l.frames = defaultLoaderFrameList()
	}
	intervalMs := defaultLoaderIntervalMs
	if indicator != nil && indicator.IntervalMs > 0 {
		intervalMs = indicator.IntervalMs
	}
	l.intervalMs = intervalMs
	l.currentFrame = 0
	l.mu.Unlock()
	l.Start()
}

func (l *Loader) restartAnimation() {
	l.mu.Lock()
	l.stopLocked()
	frames := l.frames
	intervalMs := l.intervalMs
	if len(frames) <= 1 {
		l.mu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	l.stopCh = stopCh
	l.mu.Unlock()

	go func() {
		ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				l.mu.Lock()
				l.currentFrame = (l.currentFrame + 1) % len(l.frames)
				l.mu.Unlock()
				l.updateDisplay()
			}
		}
	}()
}

func (l *Loader) updateDisplay() {
	l.mu.Lock()
	frame := ""
	if l.currentFrame < len(l.frames) {
		frame = l.frames[l.currentFrame]
	}
	renderIndicatorVerbatim := l.renderIndicatorVerbatim
	spinnerColorFn := l.spinnerColorFn
	messageColorFn := l.messageColorFn
	message := l.message
	l.mu.Unlock()

	renderedFrame := frame
	if !renderIndicatorVerbatim {
		renderedFrame = spinnerColorFn(frame)
	}
	indicator := ""
	if len(frame) > 0 {
		indicator = renderedFrame + " "
	}
	l.SetText(indicator + messageColorFn(message))
	if l.ui != nil {
		l.ui.RequestRender(false)
	}
}
