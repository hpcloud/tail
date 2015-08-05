// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"log"
	"os"
	"sync"
	"syscall"

	"github.com/hpcloud/tail/util"

	"gopkg.in/fsnotify.v0"
)

type InotifyTracker struct {
	mux     sync.Mutex
	watcher *fsnotify.Watcher
	chans   map[string]chan *fsnotify.FileEvent
	done    map[string]chan bool
	watch   chan *watchInfo
	remove  chan string
	error   chan error
}

type watchInfo struct {
	fname string
	flags uint32
}

var (
	// globally shared InotifyTracker; ensures only one fsnotify.Watcher is used
	shared *InotifyTracker

	// these are used to ensure the shared InotifyTracker is run exactly once
	once  = sync.Once{}
	goRun = func() {
		shared = &InotifyTracker{
			mux:    sync.Mutex{},
			chans:  make(map[string]chan *fsnotify.FileEvent),
			done:   make(map[string]chan bool),
			watch:  make(chan *watchInfo),
			remove: make(chan string),
			error:  make(chan error),
		}
		go shared.run()
	}

	logger = log.New(os.Stderr, "", log.LstdFlags)
)

// WatchFlags signals the run goroutine to begin watching the input filename using
// using all flags.
func Watch(fname string) error {
	return WatchFlags(fname, fsnotify.FSN_ALL)
}

// WatchFlags signals the run goroutine to begin watching the input filename using
// using the input flags.
func WatchFlags(fname string, flags uint32) error {
	// start running the shared InotifyTracker if not already running
	once.Do(goRun)

	shared.watch <- &watchInfo{
		fname: fname,
		flags: flags,
	}
	return <-shared.error
}

// RemoveWatch signals the run goroutine to remove the watch for the input filename
func RemoveWatch(fname string) {
	// start running the shared InotifyTracker if not already running
	once.Do(goRun)

	shared.mux.Lock()
	done := shared.done[fname]
	if done != nil {
		delete(shared.done, fname)
		close(done)
	}
	shared.mux.Unlock()

	shared.remove <- fname
}

// Events returns a channel to which FileEvents corresponding to the input filename
// will be sent. This channel will be closed when removeWatch is called on this
// filename.
func Events(fname string) chan *fsnotify.FileEvent {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	return shared.chans[fname]
}

// Cleanup removes the watch for the input filename if necessary.
func Cleanup(fname string) {
	RemoveWatch(fname)
}

// watchFlags calls fsnotify.WatchFlags for the input filename and flags, creating
// a new Watcher if the previous Watcher was closed.
func (shared *InotifyTracker) watchFlags(fname string, flags uint32) error {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	if shared.chans[fname] == nil {
		shared.chans[fname] = make(chan *fsnotify.FileEvent)
		shared.done[fname] = make(chan bool)
	}
	return shared.watcher.WatchFlags(fname, flags)
}

// removeWatch calls fsnotify.RemoveWatch for the input filename and closes the
// corresponding events channel.
func (shared *InotifyTracker) removeWatch(fname string) {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	if ch := shared.chans[fname]; ch != nil {
		shared.watcher.RemoveWatch(fname)

		delete(shared.chans, fname)
		close(ch)
	}
}

// sendEvent sends the input event to the appropriate Tail.
func (shared *InotifyTracker) sendEvent(event *fsnotify.FileEvent) {
	shared.mux.Lock()
	ch := shared.chans[event.Name]
	done := shared.done[event.Name]
	shared.mux.Unlock()

	if ch != nil && done != nil {
		select {
		case ch <- event:
		case <-done:
		}
	}
}

// run starts the goroutine in which the shared struct reads events from its
// Watcher's Event channel and sends the events to the appropriate Tail.
func (shared *InotifyTracker) run() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		util.Fatal("failed to create Watcher")
	}
	shared.watcher = watcher

	for {
		select {
		case winfo := <-shared.watch:
			shared.error <- shared.watchFlags(winfo.fname, winfo.flags)

		case fname := <-shared.remove:
			shared.removeWatch(fname)

		case event, open := <-shared.watcher.Event:
			if !open {
				return
			}
			shared.sendEvent(event)

		case err, open := <-shared.watcher.Error:
			if !open {
				return
			} else if err != nil {
				sysErr, ok := err.(*os.SyscallError)
				if !ok || sysErr.Err != syscall.EINTR {
					logger.Printf("Error in Watcher Error channel: %s", err)
				}
			}
		}
	}
}
