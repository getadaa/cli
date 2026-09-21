package ui

import (
	"fmt"
	"sync"
	"time"
)

// Spinner shows that something is happening. It draws only when stderr is a
// terminal; otherwise it is silent, so logs do not fill with animation frames.
type Spinner struct {
	io    *IO
	mu    sync.Mutex
	title string
	done  chan struct{}
	wg    sync.WaitGroup
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (i *IO) StartSpinner(title string) *Spinner {
	s := &Spinner{io: i, title: title, done: make(chan struct{})}
	if !i.ErrTTY {
		return s
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for n := 0; ; n++ {
			s.mu.Lock()
			fmt.Fprintf(i.Err, "\r\x1b[2K%s %s", i.E().Cyan(frames[n%len(frames)]), s.title)
			s.mu.Unlock()
			select {
			case <-s.done:
				fmt.Fprint(i.Err, "\r\x1b[2K")
				return
			case <-t.C:
			}
		}
	}()
	return s
}

func (s *Spinner) Update(title string) {
	s.mu.Lock()
	s.title = title
	s.mu.Unlock()
}

func (s *Spinner) Stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.wg.Wait()
}

// Spin runs fn behind a spinner.
func Spin[T any](i *IO, title string, fn func() (T, error)) (T, error) {
	s := i.StartSpinner(title)
	defer s.Stop()
	return fn()
}
