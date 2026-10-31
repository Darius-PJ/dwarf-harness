package agents

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
)

const loopTextSubscribers = 8
const loopTextQueue = 64
const loopTextChunk = 4 << 10

type loopLive struct {
	mu    sync.Mutex
	text  []byte
	limit int64
	index uint64
	state string
	final *ledger.ObjectRef
	subs  map[*loopTextSubscriber]bool
}
type loopTextSubscriber struct {
	channel chan session.TextDelta
	err     error
	closed  bool
}

func newLoopLive(limit int64) *loopLive {
	return &loopLive{limit: limit, state: "provisional", subs: map[*loopTextSubscriber]bool{}}
}
func (l *loopLive) stopSubscriber(sub *loopTextSubscriber, err error) {
	if sub.closed {
		return
	}
	sub.closed, sub.err = true, err
	delete(l.subs, sub)
	close(sub.channel)
}
func (l *loopLive) append(text string) error {
	if text == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != "provisional" {
		return ErrUnavailable
	}
	if !utf8.ValidString(text) || int64(len(text)) > l.limit-int64(len(l.text)) {
		l.state = "discarded"
		for sub := range l.subs {
			l.stopSubscriber(sub, ErrTooLarge)
		}
		return ErrTooLarge
	}
	for len(text) != 0 {
		n := min(len(text), loopTextChunk)
		for n < len(text) && !utf8.RuneStart(text[n]) {
			n--
		}
		chunk := strings.Clone(text[:n])
		l.text = append(l.text, chunk...)
		l.index++
		for sub := range l.subs {
			select {
			case sub.channel <- session.TextDelta{Index: l.index, Text: chunk}:
			default:
				l.stopSubscriber(sub, session.ErrReattachRequired)
			}
		}
		text = text[n:]
	}
	return nil
}
func (l *loopLive) attach() (session.TextSnapshot, *session.TextSubscription, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.subs) >= loopTextSubscribers {
		return session.TextSnapshot{}, nil, &BusyError{Reason: "view_subscribers"}
	}
	snapshot := session.TextSnapshot{Text: string(l.text), DeltaHighWater: l.index, State: l.state}
	if l.final != nil {
		snapshot.FinalMessageObject = new(*l.final)
	}
	sub := &loopTextSubscriber{channel: make(chan session.TextDelta, loopTextQueue)}
	l.subs[sub] = true
	if l.state != "provisional" {
		l.stopSubscriber(sub, session.ErrFinished)
	}
	view := session.NewTextSubscription(sub.channel, func() { l.mu.Lock(); defer l.mu.Unlock(); l.stopSubscriber(sub, session.ErrClosed) }, func() error { l.mu.Lock(); defer l.mu.Unlock(); return sub.err })
	return snapshot, view, nil
}
func (l *loopLive) close(state string, final *ledger.ObjectRef, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
	if final != nil {
		l.final = new(*final)
	}
	if err == nil {
		err = session.ErrFinished
	}
	for sub := range l.subs {
		l.stopSubscriber(sub, err)
	}
	clear(l.text)
	l.text = nil
}
