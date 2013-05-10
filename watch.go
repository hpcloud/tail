// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"github.com/howeyc/fsnotify"
	"os"
	"path/filepath"
	"time"
)

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the missing file comes into
	// existence. If the file already exists, block until it is recreated.
	BlockUntilExists() error

	// ChangeEvents returns a channel of events corresponding to the
	// times the file is ready to be read.
	ChangeEvents() chan bool
}

// InotifyFileWatcher uses inotify to monitor file changes.
type InotifyFileWatcher struct {
	Filename string
	Size     int64
}

func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher{filename, 0}
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

func (fw *InotifyFileWatcher) ChangeEvents() chan bool {
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
		defer w.Close()
		defer w.RemoveWatch(fw.Filename)
		defer close(ch)

		for {
			prevSize := fw.Size

			evt := <-w.Event
			switch {
			case evt.IsDelete():
				fallthrough

			case evt.IsRename():
				return

			case evt.IsModify():
				fi, _ := os.Stat(fw.Filename)
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					return
				}

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
	Size     int64
}

func NewPollingFileWatcher(filename string) *PollingFileWatcher {
	fw := &PollingFileWatcher{filename, 0}
	return fw
}

func (fw *PollingFileWatcher) BlockUntilExists() error {
	panic("not implemented")
	return nil
}

func (fw *PollingFileWatcher) ChangeEvents() chan bool {
	ch := make(chan bool)
	stop := make(chan bool)
	every2Seconds := time.Tick(2 * time.Second)

	var prevModTime time.Time
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}

			time.Sleep(250 * time.Millisecond)
			fi, err := os.Stat(fw.Filename)
			if err != nil {
				if os.IsNotExist(err) {
					// below goroutine (every2Seconds) will catch up
					// eventually and stop us.
					continue
				}
				panic(err)
			}

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
					stop <- true
					close(ch)
					return
				}
			}
		}
	}()

	return ch
}
