// Copyright (c) 2013 ActiveState Software Inc. All rights reserved.

package watch

import (
	"os"
	"launchpad.net/tomb"
)

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the missing file comes into
	// existence. If the file already exists, returns immediately.
	BlockUntilExists(tomb.Tomb) error

	// ChangeEvents returns a channel of events corresponding to the
	// times the file is ready to be read. The channel will be closed
	// if the file gets deleted, renamed or truncated.
	ChangeEvents(tomb.Tomb, os.FileInfo) *FileChanges
}

