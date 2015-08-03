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
	done    chan struct{}
}

var (
	shared = &InotifyTracker{
		mux:   sync.Mutex{},
		chans: make(map[string]chan *fsnotify.FileEvent),
	}
	logger = log.New(os.Stderr, "", log.LstdFlags)
)

// Watch calls fsnotify.Watch for the input filename, creating a new Watcher if the
// previous Watcher was closed.
func (shared *InotifyTracker) Watch(filename string) error {
	return shared.WatchFlags(filename, fsnotify.FSN_ALL)
}

// WatchFlags calls fsnotify.WatchFlags for the input filename and flags, creating
// a new Watcher if the previous Watcher was closed.
func (shared *InotifyTracker) WatchFlags(filename string, flags uint32) error {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	// Start up shared struct if necessary
	if len(shared.chans) == 0 {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			util.Fatal("Error creating Watcher")
		}
		shared.watcher = watcher
		shared.done = make(chan struct{})
		go shared.run()
	}

	// Create a channel to which FileEvents for the input filename will be sent
	ch := shared.chans[filename]
	if ch == nil {
		shared.chans[filename] = make(chan *fsnotify.FileEvent)
	}
	return shared.watcher.WatchFlags(filename, flags)
}

// RemoveWatch calls fsnotify.RemoveWatch for the input filename and closes the
// corresponding events channel.
func (shared *InotifyTracker) RemoveWatch(filename string) {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	_, found := shared.chans[filename]
	if !found {
		return
	}

	shared.watcher.RemoveWatch(filename)
	delete(shared.chans, filename)

	// If this is the last target to be removed, close the shared Watcher
	if len(shared.chans) == 0 {
		shared.watcher.Close()
		close(shared.done)
	}
}

// Events returns a channel to which FileEvents corresponding to the input filename
// will be sent. This channel will be closed when removeWatch is called on this
// filename.
func (shared *InotifyTracker) Events(filename string) <-chan *fsnotify.FileEvent {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	return shared.chans[filename]
}

// Cleanup removes the watch for the input filename and closes the shared Watcher
// if there are no more targets.
func Cleanup(filename string) {
	shared.RemoveWatch(filename)
}

// run starts the goroutine in which the shared struct reads events from its
// Watcher's Event channel and sends the events to the appropriate Tail.
func (shared *InotifyTracker) run() {
	for {
		select {
		case event, open := <-shared.watcher.Event:
			if !open {
				return
			}
			// send the FileEvent to the appropriate Tail's channel
			ch := shared.chans[event.Name]
			if ch != nil {
				ch <- event
			}

		case err, open := <-shared.watcher.Error:
			if !open {
				return
			} else if err != nil {
				sysErr, ok := err.(*os.SyscallError)
				if !ok || sysErr.Err != syscall.EINTR {
					logger.Printf("Error in Watcher Error channel: %s", err)
				}
			}

		case <-shared.done:
			return
		}
	}
}
