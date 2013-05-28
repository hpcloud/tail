// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"github.com/howeyc/fsnotify"
	"os"
	"path/filepath"
)

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

	dirname := filepath.Dir(fw.Filename)

	// Watch for new files to be created in the parent directory.
	err = w.WatchFlags(dirname, fsnotify.FSN_CREATE)
	if err != nil {
		return err
	}
	defer w.RemoveWatch(filepath.Dir(fw.Filename))

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	for {
		evt := <-w.Event
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
