// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"os"
	"sync"
	"time"
)

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
		} else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(POLL_DURATION)
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
			}
		}
	}()

	return ch
}

func init() {
	POLL_DURATION = 250 * time.Millisecond
}
