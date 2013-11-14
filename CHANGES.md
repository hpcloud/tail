# Nov, 2013

* add Cleanup to remove leaky inotify watches (PR #20)

# Aug, 2013

* redesigned Location field (PR #12)
* add tail.Tell (PR #14)

# July, 2013

* Rate limiting (PR #10)

# May, 2013

* Detect file deletions/renames in polling file watcher (PR #1)
* Detect file truncation
* Fix potential race condition when reopening the file (issue 5)
* Fix potential blocking of `tail.Stop` (issue 4)
* Fix uncleaned up ChangeEvents goroutines after calling tail.Stop
* Support Follow=false

# Feb, 2013

* Initial open source release
