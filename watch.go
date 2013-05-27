// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"github.com/howeyc/fsnotify"
	"os"
	"path/filepath"
	"time"
	"sync"
)

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the missing file comes into
	// existence. If the file already exists, block until it is recreated.
	BlockUntilExists() error

	// ChangeEvents returns a channel of events corresponding to the
	// times the file is ready to be read.
	ChangeEvents(os.FileInfo) chan bool
}

// InotifyFileWatcher uses inotify to monitor file changes.
type InotifyFileWatcher struct {
	Filename string
}

func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher{filename}
	return fw
}

func (fw *InotifyFileWatcher) BlockUntilExists() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	err = w.WatchFlags(filepath.Dir(fw.Filename), fsnotify.FSN_CREATE)
	if err != nil {
		return err
	}
	defer w.RemoveWatch(filepath.Dir(fw.Filename))
	for {
		evt := <-w.Event
		if evt.Name == fw.Filename {
			break
		}
	}
	return nil
}

// ChangeEvents returns a channel that gets updated when the file is ready to be read.
func (fw *InotifyFileWatcher) ChangeEvents(_ os.FileInfo) chan bool {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	err = w.Watch(fw.Filename)
	if err != nil {
		panic(err)
	}

	ch := make(chan bool)

	go func() {
		for {
			evt := <-w.Event
			switch {
			case evt.IsDelete():
				fallthrough

			case evt.IsRename():
				close(ch)
				w.RemoveWatch(fw.Filename)
				w.Close()
				return

			case evt.IsModify():
				// send only if channel is empty.
				select {
				case ch <- true:
				default:
				}
			}
		}
	}()

	return ch
}

// PollingFileWatcher polls the file for changes.
type PollingFileWatcher struct {
	Filename string
}

func NewPollingFileWatcher(filename string) *PollingFileWatcher {
	fw := &PollingFileWatcher{filename}
	return fw
}

var POLL_DURATION time.Duration

// BlockUntilExists blocks until the file comes into existence. If the
// file already exists, then block until it is created again.
func (fw *PollingFileWatcher) BlockUntilExists() error {
	for {
		if _, err := os.Stat(fw.Filename); err == nil {
			return nil
		}else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(POLL_DURATION)
		println("blocking..")
	}
}

func (fw *PollingFileWatcher) ChangeEvents(origFi os.FileInfo) chan bool {
	ch := make(chan bool)
	stop := make(chan bool)
	var once sync.Once
	every2Seconds := time.Tick(2 * time.Second)
	var prevModTime time.Time

	// XXX: use tomb.Tomb to cleanly managed these goroutines.

	stopAndClose := func() {
		go func() {
			close(ch)
			stop <- true
		}()
	}
	
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			time.Sleep(POLL_DURATION)
			fi, err := os.Stat(fw.Filename)
			if err != nil {
				if os.IsNotExist(err) {
					once.Do(stopAndClose)
					continue
				}
				/// XXX: do not panic here.
				panic(err)
			}

			// File got moved/rename within POLL_DURATION?
			if !os.SameFile(origFi, fi) {
				once.Do(stopAndClose)
				continue
			}

			// If the file was changed since last check, notify.
			modTime := fi.ModTime()
			if modTime != prevModTime {
				prevModTime = modTime
				select {
				case ch <- true:
				default:
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-every2Seconds:
				// XXX: not using file descriptor as per contract.
				if _, err := os.Stat(fw.Filename); os.IsNotExist(err) {
					once.Do(stopAndClose)
					return
				}
			}
		}
	}()

	return ch
}

func init() {
	POLL_DURATION = 250 * time.Millisecond
}
