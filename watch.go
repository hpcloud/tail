// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package tail

import (
	"github.com/howeyc/fsnotify"
	"os"
	"path/filepath"
	"time"
	"sync"
	"fmt"
)

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the missing file comes into
	// existence. If the file already exists, returns immediately.
	BlockUntilExists() error

	// ChangeEvents returns a channel of events corresponding to the
	// times the file is ready to be read.
	ChangeEvents(os.FileInfo) chan bool
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
	fmt.Println("BUE(inotify): creating watcher")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	dirname := filepath.Dir(fw.Filename)

	// Watch for new files to be created in the parent directory.
	err = w.WatchFlags(dirname, fsnotify.FSN_CREATE)
	if err != nil {
		return err
	}
	defer w.RemoveWatch(filepath.Dir(fw.Filename))

	fmt.Println("BUE(inotify): does file exist now?")
	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	fmt.Printf("BUE(inotify): checking events (last: %v)\n", err)
	for {
		evt := <-w.Event
		fmt.Printf("BUE(inotify): got event: %v\n", evt)
		if evt.Name == fw.Filename {
			break
		}
	}
	return nil
}

// ChangeEvents returns a channel that gets updated when the file is ready to be read.
func (fw *InotifyFileWatcher) ChangeEvents(fi os.FileInfo) chan bool {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	err = w.Watch(fw.Filename)
	if err != nil {
		panic(err)
	}

	ch := make(chan bool)

	fw.Size = fi.Size()

	go func() {
		defer w.Close()
		defer w.RemoveWatch(fw.Filename)
		defer close(ch)

		for {
			prevSize := fw.Size

			evt := <-w.Event
			fmt.Printf("inotify change evt: %v\n", evt)
			switch {
			case evt.IsDelete():
				fallthrough

			case evt.IsRename():
				return

			case evt.IsModify():
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					// XXX: no panic here
					panic(err)
				}
				fw.Size = fi.Size()

				fmt.Printf("WATCH(inotify): prevSize=%d; fs.Size=%d\n", prevSize, fw.Size)
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

var POLL_DURATION time.Duration

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
	panic("unreachable")
}

func (fw *PollingFileWatcher) ChangeEvents(origFi os.FileInfo) chan bool {
	ch := make(chan bool)
	stop := make(chan bool)
	var once sync.Once
	var prevModTime time.Time

	// XXX: use tomb.Tomb to cleanly manage these goroutines. replace
	// the panic (below) with tomb's Kill.

	stopAndClose := func() {
		go func() {
			close(ch)
			stop <- true
		}()
	}

	fw.Size = origFi.Size()
	
	go func() {
		prevSize := fw.Size
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

			// Was the file truncated?
			fw.Size = fi.Size()
			fmt.Printf("WATCH(poll): prevSize=%d; fs.Size=%d\n", prevSize, fw.Size)
			if prevSize > 0 && prevSize > fw.Size {
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
			}else{
				fmt.Printf("polling; not modified: %v == %v\n", modTime, prevModTime)
			}
		}
	}()

	return ch
}

func init() {
	POLL_DURATION = 250 * time.Millisecond
}
