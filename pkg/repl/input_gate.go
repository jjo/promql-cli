package repl

import (
	"io"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// altDotMarker is a private Unicode character (U+E000) used to mark Alt+. sequences
// that have been converted from ESC+. at the byte level before readline processes them.
// This allows us to distinguish between a literal "." typed by the user and Alt+.
const altDotMarker = rune(0xE000)

// rlInputGate gates stdin to readline so we can pause input while running an external editor.
var rlInputGate *inputGate

// inputGate proxies bytes from a real source (os.Stdin) to a pipe that readline consumes.
// When paused, it stops reading from the source so the editor can read directly from the TTY.
type inputGate struct {
	src                *os.File
	r                  *io.PipeReader
	w                  *io.PipeWriter
	paused             uint32 // atomic 0/1
	stop               chan struct{}
	escSeqTransformers []func([]byte) []byte // transformers for ESC sequences
}

func newInputGate(src *os.File) *inputGate {
	pr, pw := io.Pipe()
	g := &inputGate{src: src, r: pr, w: pw, stop: make(chan struct{})}
	go g.loop()
	return g
}

func (g *inputGate) Reader() io.ReadCloser { return g.r }
func (g *inputGate) Pause()                { atomic.StoreUint32(&g.paused, 1) }
func (g *inputGate) Resume()               { atomic.StoreUint32(&g.paused, 0) }
func (g *inputGate) Closed() bool          { return atomic.LoadUint32(&g.paused) == 2 }
func (g *inputGate) Close() {
	select {
	case <-g.stop:
		// already closed
	default:
		close(g.stop)
	}
	_ = g.r.Close()
	_ = g.w.Close()
	atomic.StoreUint32(&g.paused, 2)
}

func (g *inputGate) loop() {
	buf := make([]byte, 4096)
	for {
		if atomic.LoadUint32(&g.paused) == 1 {
			select {
			case <-g.stop:
				return
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		select {
		case <-g.stop:
			return
		default:
		}
		n, err := g.src.Read(buf)
		if n > 0 {
			data := buf[:n]
			// Apply any ESC sequence transformers
			for _, transform := range g.escSeqTransformers {
				data = transform(data)
			}
			_, _ = g.w.Write(data)
		}
		if err != nil {
			_ = g.w.CloseWithError(err)
			return
		}
	}
}

// Flush drains any immediately available bytes from the real stdin (TTY) without forwarding them.
func (g *inputGate) Flush() {
	if g == nil || g.src == nil {
		return
	}
	fd := int(g.src.Fd())
	_ = unix.SetNonblock(fd, true)
	defer func() { _ = unix.SetNonblock(fd, false) }()
	buf := make([]byte, 8192)
	for {
		n, err := unix.Read(fd, buf)
		if n <= 0 {
			if err == nil || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			break
		}
		// Continue until empty
	}
}
